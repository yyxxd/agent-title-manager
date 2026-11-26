package orcasync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func TestCodexRecoveryRequiresNativeAckAndLiveIdentity(t *testing.T) {
	const nativeTitle, previous, manual = "Native title", "Previous title", "Manual title"
	for _, tc := range []struct {
		name, kind, native, orca string
		ack, sameBinding, attach bool
	}{
		{"unacknowledged", "project", "", previous, false, true, false},
		{"native not written", "project", "", previous, true, true, false},
		{"ambiguous binding", "bot", nativeTitle, previous, true, false, false},
		{"ordinary readback", "project", nativeTitle, nativeTitle, true, true, true},
		{"bot manual title", "bot", nativeTitle, manual, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			row := codexSettlement{orcaID: acpChat, nativeID: acpNative, account: root, revision: "turn"}
			seenNative := false
			i := Integration{Enabled: alwaysEnabled, origin: func(context.Context, string, string, string) string {
				if tc.orca == manual {
					return titleManual
				}
				return titleAuto
			}}
			i.run = func(_ context.Context, args ...string) ([]byte, error) {
				if !seenNative || args[0] != "agent" || args[1] != "show" {
					t.Fatalf("unexpected Orca call before native proof: %v", args)
				}
				switch args[2] {
				case row.nativeID:
					if !tc.sameBinding {
						return providerMapping(manualChat)
					}
					return providerMapping(row.orcaID)
				case idPrefix + row.orcaID:
					return replyPlace(place{ID: row.orcaID, Agent: "codex", Kind: "chat", HostID: "local", Title: &tc.orca})
				default:
					return nil, fmt.Errorf("unexpected identity: %v", args)
				}
			}
			attached := 0
			r := codexBindingRecovery{
				find: func(_ context.Context, agent, session, revision string) (engine.ConfirmedBinding, error) {
					if agent != "codex" || session != row.nativeID || revision != row.revision {
						t.Fatalf("wrong native receipt identity: %s %s %s", agent, session, revision)
					}
					if !tc.ack {
						return engine.ConfirmedBinding{}, nil
					}
					return engine.ConfirmedBinding{ID: "confirmed", NativeTitle: nativeTitle}, nil
				},
				read: func(_ context.Context, actual codexSettlement) (string, error) {
					if actual != row {
						t.Fatalf("wrong scoped native read: %+v", actual)
					}
					seenNative = true
					return tc.native, nil
				},
				attach: func(_ context.Context, evidence engine.ConfirmedBinding) error {
					if !evidence.ReadbackOnly || evidence.NativeTitle != nativeTitle ||
						evidence.OrcaID != row.orcaID || evidence.OrcaTitle != tc.orca {
						t.Fatalf("invalid readback evidence: %+v", evidence)
					}
					attached++
					return nil
				},
			}
			_, err := i.recoverCodexBinding(t.Context(), r, row)
			if (err != nil) != (!tc.ack || !tc.sameBinding) || seenNative != tc.ack || (attached == 1) != tc.attach {
				t.Fatalf("read=%v attach=%d err=%v", seenNative, attached, err)
			}
		})
	}
}

func TestBotSideChatUsesItsOwnTitleInsteadOfBotName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ORCA_USER_DATA_PATH", root)
	db := makeACPDatabase(t, root)
	record := fmt.Sprintf(`{"schemaVersion":2,"sessionId":%q,"provider":"codex","location":{"executionHostId":"local","workspaceKind":"folder"}}`, acpChat)
	if _, err := db.Exec(`INSERT INTO agent_session_records VALUES (?,?)`, acpChat, record); err != nil {
		t.Fatal(err)
	}
	bots, err := sql.Open("sqlite", filepath.Join(root, "orchestration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bots.Close()
	if _, err = bots.Exec(`CREATE TABLE bot_conversation_acts_for(session_id TEXT);
		INSERT INTO bot_conversation_acts_for VALUES (?)`, acpChat); err != nil {
		t.Fatal(err)
	}
	botName, current := "Fixture Bot", "Old topic title"
	at := place{ID: acpChat, Agent: "codex", Kind: "chat", HostID: "local", Title: &botName}
	writes := 0
	run := func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] + " " + args[1] {
		case "agent show":
			if args[2] == acpNative {
				return providerMapping(acpChat)
			}
			return replyPlace(at)
		case "agent list":
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"agents": []place{at}, "truncated": false}})
		case "bot list":
			return []byte(`{"ok":true,"result":{"bots":[{"id":"fixture-bot","name":"Fixture Bot"}]}}`), nil
		case "bot chats":
			chats := []map[string]string{{"id": acpChat, "title": current}, {"id": manualChat, "title": "Sibling title"}}
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"chats": chats}})
		case "chat rename":
			if args[3] != acpChat {
				t.Fatalf("sibling or Bot renamed: %v", args)
			}
			writes++
			current = args[5]
			return []byte(`{"ok":true}`), nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	i := Integration{Enabled: alwaysEnabled, run: run, ReadNative: func(context.Context, string, string, string) (string, error) {
		return "Confirmed native title", nil
	}}
	binding, ok := i.ResolveTitleOrigin(t.Context(), "codex", acpNative, acpNative, acpChat)
	if !ok || binding.Title != current || binding.Kind != "bot" {
		t.Fatalf("Bot topic bound to displayed Bot name: %+v", binding)
	}
	if err := i.SyncVerified(t.Context(), acpChat, current, "Confirmed native title", "codex", acpNative, acpNative); err != nil ||
		writes != 1 || current != "Confirmed native title" || botName != "Fixture Bot" {
		t.Fatalf("wrong Bot title sync: title=%q, writes=%d, err=%v", current, writes, err)
	}
}

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
			"providerHandleChain": []any{map[string]any{"origin": "created", "handle": map[string]string{"provider": "codex", "threadId": item.native}}},
			"lastTurn":            map[string]string{"state": "settled", "outcome": "success"},
			"conversationName":    "Fixture title",
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

	title := "Acknowledged native title"
	i := Integration{Enabled: alwaysEnabled, run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[2] == acpNative {
			return providerMapping(acpChat)
		}
		return replyPlace(place{ID: acpChat, Agent: "codex", Kind: "chat", HostID: "local", Title: &title})
	}, origin: func(context.Context, string, string, string) string { return titleAuto }}
	polls, attached := 0, 0
	recover := codexBindingRecovery{
		find: func(_ context.Context, _, session, _ string) (engine.ConfirmedBinding, error) {
			if session != acpNative {
				return engine.ConfirmedBinding{}, nil
			}
			polls++
			if polls <= 4 {
				return engine.ConfirmedBinding{}, nil
			}
			return engine.ConfirmedBinding{ID: "confirmed", NativeTitle: title}, nil
		},
		read: func(_ context.Context, row codexSettlement) (string, error) {
			if row.nativeID != acpNative || polls != 5 {
				t.Fatalf("unexpected read before or after native acknowledgement: %+v", row)
			}
			return title, nil
		},
		attach: func(_ context.Context, proof engine.ConfirmedBinding) error {
			if proof.OrcaID != acpChat || !proof.ReadbackOnly {
				t.Fatalf("unexpected attachment: %+v", proof)
			}
			attached++
			return nil
		},
	}
	observed := make(map[[2]string]codexObservation)
	for tick := range 6 {
		i.observeCodexSettlements(t.Context(), recover, observed, time.UnixMilli(50),
			time.UnixMilli(90).Add(codexHookGrace+time.Duration(tick)*3*time.Second))
	}
	if polls != 5 || attached != 1 {
		t.Fatalf("expected one attachment after four pending polls, got polls=%d attachments=%d", polls, attached)
	}
}
