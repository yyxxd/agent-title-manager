package orcasync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	acpChat      = "e139bb8d-a50a-4de6-8bef-6fa674f005c1"
	acpNative    = "b68b5391-bfed-45ae-948e-259bb4abf012"
	manualChat   = "e139bb8d-a50a-4de6-8bef-6fa674f005c2"
	manualNative = "b68b5391-bfed-45ae-948e-259bb4abf013"
)

func TestACPObserverSettlesRenamesAndProtectsManualName(t *testing.T) {
	testACPObserver(t, t.TempDir(), "")
}

func testACPObserver(t *testing.T, home, account string) {
	t.Helper()
	node, err := exec.LookPath("node.exe")
	if err != nil {
		node, err = exec.LookPath("node")
		if err != nil {
			t.Fatal("Node.js required to exercise embedded observation:", err)
		}
	}
	plugin, err := filepath.Abs("../../plugins/antigravity")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORCA_USER_DATA_PATH", home)
	t.Setenv("NODE_TEST_CONTEXT", "isolated-acp-fixture")
	if account == "" {
		account = filepath.Join(home, "antigravity-chat")
	}
	data := filepath.Join(account, "antigravity-acp")
	db := makeACPDatabase(t, home)
	now := time.Now()
	putACPFixture(t, db, data, account, acpChat, acpNative, "", now)
	putACPFixture(t, db, data, account, manualChat, manualNative, "user", now)
	putACPFixture(t, db, data, filepath.Join(home, "other-account"), "e139bb8d-a50a-4de6-8bef-6fa674f005c3", "b68b5391-bfed-45ae-948e-259bb4abf014", "", now)

	title, writes := "Antigravity Chat", 0
	const target = "fixture result"
	i := Integration{Enabled: alwaysEnabled, ReadNative: func(context.Context, string, string, string) (string, error) {
		return "", errors.New("ACP used unscoped title read")
	}, ReadScoped: func(_ context.Context, id, root string) (string, error) {
		if id != acpNative || !samePath(root, data) {
			return "", fmt.Errorf("unverified native root")
		}
		wire, err := os.ReadFile(filepath.Join(root, "annotations", id+".pbtxt"))
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(string(wire), "title:")), "\""), err
	}}
	i.run = func(_ context.Context, args ...string) ([]byte, error) {
		at := place{ID: acpChat, Agent: "antigravity", Kind: "chat", HostID: "local", Title: &title}
		switch strings.Join(args[:2], " ") {
		case "agent show":
			if args[2] == acpNative {
				return providerMapping(acpChat)
			}
			if args[2] != idPrefix+acpChat {
				return nil, fmt.Errorf("unverified chat lookup: %v", args)
			}
			return replyPlace(at)
		case "agent list":
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"agents": []place{at}, "truncated": false}})
		case "chat rename":
			if args[2] != "--session" || args[3] != acpChat || args[4] != "--name" || args[5] != target {
				return nil, fmt.Errorf("rename target: %v", args)
			}
			writes++
			title = args[5]
			return []byte(`{"ok":true}`), nil
		default:
			return nil, fmt.Errorf("unexpected command: %v", args)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	var revision string
	var observed error
	engine := func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		var fields map[string]string
		_ = json.Unmarshal(raw, &fields)
		if fields["sessionId"] != acpNative {
			return nil, fmt.Errorf("observer admitted unrelated session %s", fields["sessionId"])
		}
		switch method {
		case "event.submit":
			if fields["orcaSessionId"] != acpChat || fields["orcaTitleOrigin"] != "auto" {
				return nil, fmt.Errorf("unverified ACP observation: %s", raw)
			}
			revision = fields["revision"]
			return map[string]bool{"accepted": true}, nil
		case "title.claim":
			return map[string]any{"candidate": map[string]string{"id": "record", "title": target, "revision": revision, "baseTitle": ""}}, nil
		case "title.ack":
			observed = i.SyncVerified(ctx, acpChat, "Antigravity Chat", fields["observedTitle"], "antigravity", acpNative, acpNative)
			cancel()
			return map[string]bool{"accepted": true}, observed
		}
		return nil, fmt.Errorf("unexpected method %s", method)
	}
	launch := func(ctx context.Context, args ...string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, node, args...)
		cmd.Stderr = os.Stderr
		return cmd, nil
	}
	if err := i.RunACPObserver(ctx, plugin, launch, engine); ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("observer did not complete: %v; rename: %v; writes=%d", err, observed, writes)
	}
	if observed != nil || writes != 1 || title != target {
		t.Fatalf("sync: %v; %d writes; %q", observed, writes, title)
	}
	if _, err := os.Stat(filepath.Join(data, "annotations", manualNative+".pbtxt")); !os.IsNotExist(err) {
		t.Fatalf("observer touched manual name: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_session_records SET record_json=json_set(record_json,'$.conversationNameSource','user') WHERE session_id=?`, acpChat); err != nil {
		t.Fatal(err)
	}
	title = "manually changed"
	err = i.SyncVerified(t.Context(), acpChat, title, target, "antigravity", acpNative, acpNative)
	if !errors.Is(err, ErrSkipped) || writes != 1 {
		t.Fatalf("manual title protection: %v; %d writes", err, writes)
	}
}

func makeACPDatabase(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(home, "agent-session-domain.db"), nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec(`CREATE TABLE agent_session_records (session_id TEXT PRIMARY KEY, record_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func putACPFixture(t *testing.T, db *sql.DB, data, account, orca, native, source string, now time.Time) {
	t.Helper()
	path := filepath.Join(data, "brain", native, ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-10 * time.Millisecond).Format(time.RFC3339Nano)
	wire := fmt.Sprintf("{\"type\":\"USER_INPUT\",\"source\":\"USER_EXPLICIT\",\"status\":\"DONE\",\"step_index\":3,\"content\":\"request\",\"created_at\":%q}\n"+
		"{\"type\":\"PLANNER_RESPONSE\",\"source\":\"MODEL\",\"status\":\"DONE\",\"step_index\":6,\"content\":\"answer\",\"created_at\":%q}\n", at, at)
	if err := os.WriteFile(path, []byte(wire), 0600); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`{"sessionId":%q,"schemaVersion":2,"provider":"antigravity","location":{"executionHostId":"local"},
		"accountHome":{"variable":"GEMINI_HOME","path":%q},"providerHandleChain":[{"handle":{"transport":"acp","agent":"antigravity","nativeId":%q}}],
		"lastTurn":{"state":"settled","outcome":"success","at":%d}`, orca, account, native, now.UnixMilli())
	if source != "" {
		record += fmt.Sprintf(`,"conversationNameSource":%q`, source)
	}
	if _, err := db.Exec(`INSERT INTO agent_session_records VALUES (?,?)`, orca, record+"}"); err != nil {
		t.Fatal(err)
	}
}
