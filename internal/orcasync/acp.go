package orcasync

import (
	"bufio"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed acp-observer.mjs
var acpObserver string

//go:embed acp-rpc.mjs
var acpRPC string

type NodeCommand func(context.Context, ...string) (*exec.Cmd, error)
type Handler func(context.Context, string, json.RawMessage) (any, error)

func (i *Integration) RunACPObserver(ctx context.Context, dir string, node NodeCommand, engine Handler) error {
	if !i.Active(ctx, "antigravity") {
		return nil
	}
	db := orcaDatabase()
	if db == "" {
		return errors.New("无法定位 Orca 配置目录")
	}
	ctx, cancel := i.operation(ctx, "antigravity")
	defer cancel()
	cmd, err := node(ctx, "--input-type=module", "-e", acpRPC+"\n"+acpObserver, dir, filepath.Dir(db))
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	send := json.NewEncoder(stdin)
	var loopErr error
	for scanner.Scan() {
		if loopErr = i.acpRequest(ctx, scanner.Bytes(), send, engine); loopErr != nil {
			cancel()
			break
		}
	}
	return errors.Join(loopErr, scanner.Err(), cmd.Wait())
}

func (i *Integration) acpRequest(ctx context.Context, wire []byte, send *json.Encoder, engine Handler) error {
	var msg struct {
		ID     int                        `json:"id"`
		OrcaID string                     `json:"orcaSessionId"`
		Method string                     `json:"method"`
		Params map[string]json.RawMessage `json:"params"`
	}
	if json.Unmarshal(wire, &msg) != nil || msg.ID < 1 {
		return errors.New("Orca Chat 观察器请求无效")
	}
	var result any
	var err error
	if msg.Method == "settlements.list" {
		var since int64
		if json.Unmarshal(msg.Params["since"], &since) != nil {
			return errors.New("Orca Chat 观察器时间范围无效")
		}
		result, err = i.readACPSettlements(ctx, since)
	} else if nativeID.MatchString(msg.OrcaID) {
		result, err = i.handleACP(ctx, msg.OrcaID, msg.Method, msg.Params, engine)
	} else {
		return errors.New("Orca Chat 观察器会话 ID 无效")
	}
	reply := map[string]any{"id": msg.ID, "result": result}
	if err != nil {
		reply["error"] = err.Error()
	}
	return send.Encode(reply)
}

func (i *Integration) handleACP(ctx context.Context, id, method string, fields map[string]json.RawMessage, engine Handler) (any, error) {
	var agent, session string
	if json.Unmarshal(fields["agent"], &agent) != nil || agent != "antigravity" ||
		json.Unmarshal(fields["sessionId"], &session) != nil || !nativeID.MatchString(session) || !i.Active(ctx, agent) {
		return nil, ErrSkipped
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch method {
	case "event.submit":
		bound, ok := i.resolve(ctx, agent, session, session, id, "chat", []time.Duration{0})
		if !ok || bound.Origin == titleManual {
			return map[string]bool{"accepted": false}, nil
		}
		for key, value := range map[string]string{"orcaSessionId": bound.ID, "orcaNativeHandle": bound.Handle, "orcaTitle": bound.Title, "orcaTitleOrigin": bound.Origin} {
			fields[key], _ = json.Marshal(value)
		}
	case "title.claim", "title.ack":
	default:
		return nil, errors.New("Orca Chat 观察器方法无效")
	}
	params, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return engine(ctx, method, params)
}

func acpRoot(ctx context.Context, id, session string) (string, error) {
	id = strings.TrimPrefix(id, idPrefix)
	if !nativeID.MatchString(id) || !nativeID.MatchString(session) {
		return "", ErrNativePending
	}
	path := orcaDatabase()
	db, err := openOrcaDatabase(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var account string
	err = db.QueryRowContext(ctx, `
		SELECT json_extract(record_json, '$.accountHome.path')
		FROM agent_session_records
		WHERE session_id = ? AND json_extract(record_json, '$.sessionId') = session_id
		  AND json_extract(record_json, '$.schemaVersion') = 2
		  AND json_extract(record_json, '$.provider') = 'antigravity'
		  AND json_extract(record_json, '$.location.executionHostId') = 'local'
		  AND json_extract(record_json, '$.accountHome.variable') = 'GEMINI_HOME'
		  AND json_extract(record_json, '$.providerHandleChain[#-1].handle.transport') = 'acp'
		  AND json_extract(record_json, '$.providerHandleChain[#-1].handle.agent') = 'antigravity'
		  AND json_extract(record_json, '$.providerHandleChain[#-1].handle.nativeId') = ?`, id, session).Scan(&account)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(path), "antigravity-chat")
	if !filepath.IsAbs(account) || !samePath(account, dir) {
		return "", fmt.Errorf("%w: 无效的 Antigravity 账号目录", ErrNativePending)
	}
	return filepath.Join(dir, "antigravity-acp"), nil
}

type settlement struct {
	Orca   string `json:"orca"`
	Native string `json:"native"`
	At     int64  `json:"at"`
}

func (i *Integration) readACPSettlements(ctx context.Context, since int64) ([]settlement, error) {
	result := []settlement{}
	if !i.Active(ctx, "antigravity") {
		return result, ErrSkipped
	}
	path := orcaDatabase()
	db, err := openOrcaDatabase(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rows, err := db.QueryContext(ctx, `
		SELECT session_id, json_extract(record_json, '$.providerHandleChain[#-1].handle.nativeId'),
		       json_extract(record_json, '$.lastTurn.at'), json_extract(record_json, '$.accountHome.path')
		FROM agent_session_records
		WHERE json_extract(record_json, '$.schemaVersion') = 2
		  AND json_extract(record_json, '$.sessionId') = session_id
		  AND json_extract(record_json, '$.provider') = 'antigravity'
		  AND json_extract(record_json, '$.location.executionHostId') = 'local'
		  AND json_extract(record_json, '$.accountHome.variable') = 'GEMINI_HOME'
		  AND json_extract(record_json, '$.providerHandleChain[#-1].handle.transport') = 'acp'
		  AND json_extract(record_json, '$.providerHandleChain[#-1].handle.agent') = 'antigravity'
		  AND json_extract(record_json, '$.lastTurn.state') = 'settled'
		  AND json_extract(record_json, '$.lastTurn.outcome') = 'success'
		  AND coalesce(json_extract(record_json, '$.conversationNameSource'), '') <> 'user'
		  AND json_type(record_json, '$.lastTurn.at') = 'integer'
		  AND json_extract(record_json, '$.lastTurn.at') BETWEEN ? AND ?
		ORDER BY json_extract(record_json, '$.lastTurn.at') DESC LIMIT 64`,
		max(since, time.Now().Add(-5*time.Minute).UnixMilli()), time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	expected := filepath.Join(filepath.Dir(path), "antigravity-chat")
	for rows.Next() {
		var item settlement
		var native, account sql.NullString
		if err := rows.Scan(&item.Orca, &native, &item.At, &account); err != nil {
			return nil, err
		}
		if nativeID.MatchString(item.Orca) && nativeID.MatchString(native.String) &&
			filepath.IsAbs(account.String) && samePath(account.String, expected) {
			item.Native = native.String
			result = append(result, item)
		}
	}
	return result, rows.Err()
}

func openOrcaDatabase(path string) (*sql.DB, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	if !safeDatabase(path) {
		return nil, errors.New("Orca 数据库文件路径无效")
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(200)"}}
	return sql.Open("sqlite", databaseURI(path, query))
}

func safeDatabase(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		file := path + suffix
		info, err := os.Lstat(file)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || !unredirectedPath(file, info) {
			return false
		}
	}
	return true
}
