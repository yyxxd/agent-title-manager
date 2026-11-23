package orcasync

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

//go:embed codex-turn.mjs
var codexTurnScript string

const codexHookGrace = 8 * time.Second
const codexObservationWindow = 3 * time.Minute

type codexSettlement struct {
	orcaID, nativeID, revision, account string
	at                                  int64
}

type codexObservation struct {
	tries int
	at    int64
}

func (i *Integration) RunCodexObserver(parent context.Context, dir string, node NodeCommand, engine Handler) error {
	if !i.Active(parent, "codex") {
		return nil
	}
	ctx, cancel := i.operation(parent, "codex")
	defer cancel()
	started := time.Now()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	observed := make(map[[2]string]codexObservation)
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-tick.C:
			i.observeCodexSettlements(ctx, dir, node, engine, observed, started, now)
		}
	}
}

func (i *Integration) observeCodexSettlements(ctx context.Context, dir string, node NodeCommand, engine Handler,
	observed map[[2]string]codexObservation, started, now time.Time) {
	since := max(started.UnixMilli(), now.Add(-codexObservationWindow).UnixMilli())
	for key, item := range observed {
		if item.at < since {
			delete(observed, key)
		}
	}
	rows, err := readCodexSettlements(ctx, orcaDatabase(), since, now.Add(-codexHookGrace).UnixMilli())
	if err != nil && ctx.Err() == nil {
		log.Print("无法读取 Orca Codex 已完成回合")
		return
	}
	for _, row := range rows {
		key := [2]string{row.nativeID, row.revision}
		item := observed[key]
		if item.tries >= 3 || !i.Active(ctx, "codex") {
			continue
		}
		item.at, item.tries = row.at, item.tries+1
		done, err := i.submitCodexSettlement(ctx, dir, node, engine, row)
		if err != nil && item.tries == 3 && ctx.Err() == nil {
			log.Print("Orca Codex 回合未能确认并入库")
		}
		if done {
			item.tries = 3
		}
		observed[key] = item
	}
}

func readCodexSettlements(ctx context.Context, path string, since, before int64) ([]codexSettlement, error) {
	if before < since {
		return nil, nil
	}
	db, err := openOrcaDatabase(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rows, err := db.QueryContext(ctx, `
		SELECT r.session_id,json_extract(r.record_json,'$.providerHandleChain[#-1].handle.threadId'),
			j.provider_turn_id,json_extract(r.record_json,'$.accountHome.path'),CAST(j.turn_completed_at AS INTEGER)
		FROM journal_items j JOIN agent_session_records r ON r.session_id=j.session_id
		WHERE j.kind='turn' AND j.lifecycle_state='completed' AND j.turn_completed_at BETWEEN ? AND ?
		  AND j.turn_completed_at=(SELECT MAX(newer.turn_completed_at) FROM journal_items newer
			    WHERE newer.session_id=j.session_id AND newer.kind='turn' AND newer.lifecycle_state='completed')
		  AND json_extract(r.record_json,'$.schemaVersion')=2
		  AND json_extract(r.record_json,'$.sessionId')=r.session_id
		  AND json_extract(r.record_json,'$.provider')='codex'
		  AND json_extract(r.record_json,'$.location.executionHostId')='local'
		  AND json_extract(r.record_json,'$.accountHome.variable')='CODEX_HOME'
		  AND json_extract(r.record_json,'$.providerHandleChain[#-1].handle.provider')='codex'
		  AND json_extract(r.record_json,'$.lastTurn.state')='settled'
		  AND json_extract(r.record_json,'$.lastTurn.outcome')='success'
		  AND (j.codex_thread_id IS NULL OR j.codex_thread_id=json_extract(r.record_json,'$.providerHandleChain[#-1].handle.threadId'))
		  AND (j.codex_turn_id IS NULL OR j.codex_turn_id=j.provider_turn_id)
		ORDER BY j.turn_completed_at DESC LIMIT 64`, since, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []codexSettlement
	for rows.Next() {
		var r codexSettlement
		var native, revision, account sql.NullString
		if err := rows.Scan(&r.orcaID, &native, &revision, &account, &r.at); err != nil {
			return nil, err
		}
		if nativeID.MatchString(r.orcaID) && nativeID.MatchString(native.String) && sessionID.MatchString(revision.String) &&
			filepath.IsAbs(account.String) {
			r.nativeID, r.revision, r.account = native.String, revision.String, account.String
			result = append(result, r)
		}
	}
	return result, rows.Err()
}

func (i *Integration) submitCodexSettlement(ctx context.Context, dir string, node NodeCommand, engine Handler, row codexSettlement) (bool, error) {
	id := map[string]string{"agent": "codex", "sessionId": row.nativeID, "revision": row.revision}
	body, err := json.Marshal(id)
	if err != nil {
		return true, err
	}
	result, err := engine(ctx, "event.eligibility", body)
	if err != nil {
		return false, err
	}
	var state struct {
		Seen, Locked bool
		Epoch        *int64
	}
	wire, err := json.Marshal(result)
	if err != nil || json.Unmarshal(wire, &state) != nil || state.Epoch == nil {
		return false, errors.New("无法确认 Codex 回合登记状态")
	}
	if state.Seen || state.Locked {
		return true, nil
	}
	info, err := os.Lstat(row.account)
	if err != nil || !info.IsDir() || !unredirectedPath(row.account, info) {
		return false, errors.New("Codex 回合的账户目录不可确认")
	}
	bound, ok := i.resolve(ctx, "codex", row.nativeID, row.nativeID, row.orcaID, "chat", []time.Duration{0})
	if !ok {
		return false, errors.New("Codex 回合的 Orca 绑定不可确认")
	}
	snapshot, err := collectCodexTurn(ctx, dir, node, row)
	if err != nil || snapshot == nil || !i.Active(ctx, "codex") {
		return false, err
	}
	for key, value := range map[string]any{
		"agent": "codex", "sessionId": row.nativeID, "revision": row.revision,
		"orcaSessionId": bound.ID, "orcaNativeHandle": bound.Handle, "orcaTitle": bound.Title, "orcaTitleOrigin": bound.Origin,
		"sessionKind": bound.Kind, "protectionEpoch": *state.Epoch, "reviewOnly": true,
	} {
		snapshot[key] = value
	}
	params, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	_, err = engine(ctx, "event.submit", params)
	return err == nil, err
}

func collectCodexTurn(ctx context.Context, dir string, node NodeCommand, row codexSettlement) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd, err := node(ctx, "--input-type=module", "-e", codexTurnScript, dir, row.account, row.nativeID, row.revision)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if len(out) > 64<<10 || json.Unmarshal(out, &snapshot) != nil {
		return nil, errors.New("Codex 原生回合读回格式无效")
	}
	return snapshot, nil
}
