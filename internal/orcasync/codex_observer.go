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

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

//go:embed codex-title.mjs
var codexTitleScript string

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

var errCodexAckPending = errors.New("等待 Codex 原生回执")

type codexBindingRecovery struct {
	find   func(context.Context, string, string, string) (engine.ConfirmedBinding, error)
	attach func(context.Context, engine.ConfirmedBinding) error
	read   func(context.Context, codexSettlement) (string, error)
}

func (i *Integration) RunCodexObserver(parent context.Context, dir string, node NodeCommand, core *engine.Engine) error {
	recover := codexBindingRecovery{
		find: core.UnboundAppliedTitle, attach: core.AttachBinding,
		read: func(ctx context.Context, row codexSettlement) (string, error) {
			return readCodexTitle(ctx, dir, node, row)
		},
	}
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
			i.observeCodexSettlements(ctx, recover, observed, started, now)
		}
	}
}

func (i *Integration) observeCodexSettlements(ctx context.Context, recover codexBindingRecovery,
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
		item.at = row.at
		done, err := i.recoverCodexBinding(ctx, recover, row)
		if !errors.Is(err, errCodexAckPending) {
			item.tries++
			if err != nil && item.tries == 3 && ctx.Err() == nil {
				log.Print("Orca Codex 回合未能确认并入库")
			}
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

func (i *Integration) recoverCodexBinding(ctx context.Context, recover codexBindingRecovery, row codexSettlement) (bool, error) {
	applied, err := recover.find(ctx, "codex", row.nativeID, row.revision)
	if err != nil {
		return false, err
	}
	if applied.ID == "" {
		return false, errCodexAckPending
	}
	info, err := os.Lstat(row.account)
	if err != nil || !info.IsDir() || !unredirectedPath(row.account, info) {
		return false, errors.New("无法确认 Codex 回合的账户目录")
	}
	title, err := recover.read(ctx, row)
	if err != nil || !i.Active(ctx, "codex") {
		return false, err
	}
	if title != applied.NativeTitle {
		return true, nil
	}
	bound, ok := i.resolve(ctx, "codex", row.nativeID, row.nativeID, row.orcaID, "chat", []time.Duration{0})
	if !ok {
		return false, errors.New("无法确认 Codex 回合的 Orca 绑定")
	}
	applied.OrcaID, applied.OrcaTitle, applied.Origin = bound.ID, bound.Title, bound.Origin
	applied.NativeHandle, applied.Kind, applied.ReadbackOnly = bound.Handle, bound.Kind, true
	err = recover.attach(ctx, applied)
	return err == nil, err
}

func readCodexTitle(ctx context.Context, dir string, node NodeCommand, row codexSettlement) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd, err := node(ctx, "--input-type=module", "-e", codexTitleScript, dir, row.account, row.nativeID)
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var title *string
	if len(out) > 8192 || json.Unmarshal(out, &title) != nil || title == nil {
		return "", errors.New("Codex 原生标题读回无效")
	}
	return *title, nil
}
