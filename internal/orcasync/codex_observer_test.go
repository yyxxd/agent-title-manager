package orcasync

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCodexSettlementsGiveEachConversationItsOwnLatestNativeTurn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ORCA_USER_DATA_PATH", root)
	db, err := sql.Open("sqlite", filepath.Join(root, "agent-session-domain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE agent_session_records(session_id TEXT PRIMARY KEY,record_json TEXT);
		CREATE TABLE journal_items(session_id TEXT,kind TEXT,lifecycle_state TEXT,turn_completed_at REAL,
			provider_turn_id TEXT,codex_thread_id TEXT,codex_turn_id TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	bots, err := sql.Open("sqlite", filepath.Join(root, "orchestration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bots.Close()
	if _, err = bots.Exec(`CREATE TABLE bot_conversation_acts_for(session_id TEXT PRIMARY KEY,bot_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, native, kind, host string
		at                     int
	}{
		{acpChat, acpNative, "project", "local", 70},
		{manualChat, manualNative, "bot", "local", 76},
		{"e139bb8d-a50a-4de6-8bef-6fa674f005c3", "b68b5391-bfed-45ae-948e-259bb4abf014", "bot", "local", 80},
		{"e139bb8d-a50a-4de6-8bef-6fa674f005c4", "b68b5391-bfed-45ae-948e-259bb4abf015", "project", "remote", 85},
		{"e139bb8d-a50a-4de6-8bef-6fa674f005c5", "b68b5391-bfed-45ae-948e-259bb4abf016", "project", "local", 49},
	} {
		wire, err := json.Marshal(map[string]any{
			"schemaVersion": 2, "sessionId": item.id, "provider": "codex",
			"location":            map[string]string{"executionHostId": item.host, "workspaceKind": "git-worktree"},
			"accountHome":         map[string]string{"variable": "CODEX_HOME", "path": root},
			"providerHandleChain": []any{map[string]any{"handle": map[string]string{"provider": "codex", "threadId": item.native}}},
			"lastTurn":            map[string]string{"state": "settled", "outcome": "success"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO agent_session_records VALUES(?,?)`, item.id, wire); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO journal_items VALUES(?,'turn','completed',?,'turn-current',?,'turn-current'),
			(?,'turn','completed',?,'turn-before',?,'turn-before')`, item.id, item.at, item.native, item.id, item.at-1, item.native); err != nil {
			t.Fatal(err)
		}
		if item.kind == "bot" {
			if _, err := bots.Exec(`INSERT INTO bot_conversation_acts_for VALUES(?,'same-bot')`, item.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	items, err := readCodexSettlements(t.Context(), orcaDatabase(), 50, 90)
	if err != nil || len(items) != 3 {
		t.Fatalf("expected three current conversations, got %+v: %v", items, err)
	}
	order := []string{"bot", "bot", "project"}
	ids := map[string]bool{}
	for index, item := range items {
		if item.revision != "turn-current" || ids[item.nativeID] || codexSessionKind(t.Context(), item.orcaID) != order[index] {
			t.Fatalf("wrong conversation binding: %+v", item)
		}
		ids[item.nativeID] = true
	}
}
