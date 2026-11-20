package orcasync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestAntigravityChatRenameRechecksUserSourceAfterNativeRead(t *testing.T) {
	const initial, target = "Antigravity Chat", "规范标题"
	for _, tc := range []struct {
		name, visible, fields   string
		markUser, nativeChanged bool
		want                    error
		writes                  int
	}{
		{name: "default is eligible", visible: initial, writes: 1},
		{name: "auto source is eligible", visible: "Previous automatic", fields: `,"conversationName":"Previous automatic"`, writes: 1},
		{name: "manual same as default", visible: initial, fields: `,"conversationName":"Antigravity Chat","conversationNameSource":"user"`, want: ErrSkipped},
		{name: "manual source set during native read", visible: initial, markUser: true, want: ErrSkipped},
		{name: "native readback changed", visible: initial, nativeChanged: true, want: ErrNativeStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ORCA_USER_DATA_PATH", t.TempDir())
			db, err := sql.Open("sqlite", databaseURI(orcaDatabase(), nil))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE agent_session_records (session_id TEXT PRIMARY KEY, record_json TEXT)`); err != nil {
				t.Fatal(err)
			}
			put := func(fields string) {
				t.Helper()
				const record = `{"schemaVersion":2,"sessionId":"codex_chat","provider":"antigravity"`
				if _, err := db.Exec(`INSERT OR REPLACE INTO agent_session_records VALUES ('codex_chat', ?)`, record+fields+"}"); err != nil {
					t.Fatal(err)
				}
			}
			put(tc.fields)
			f := &cliFixture{t: t, title: tc.visible, write: true, agent: "antigravity"}
			nativeReads := 0
			reader := func(_ context.Context, agent, id, handle string) (string, error) {
				if agent != "antigravity" || id != nativeFixture || handle != id {
					t.Fatal("unchecked native identity")
				}
				nativeReads++
				if tc.markUser {
					put(`,"conversationName":"Antigravity Chat","conversationNameSource":"user"`)
				}
				if tc.nativeChanged {
					return "native manual title", nil
				}
				return target, nil
			}
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				if args[0] == "agent" && args[1] == "show" && args[2] == nativeFixture {
					return providerMapping("codex_chat")
				}
				if args[0] == "chat" && nativeReads == 0 {
					t.Fatal("chat rename preceded native readback")
				}
				return f.run(ctx, args...)
			}
			err = syncVerified(context.Background(), run, "codex_chat", tc.visible, target, "antigravity", nativeFixture, nativeFixture, reader)
			if !errors.Is(err, tc.want) || f.writes != tc.writes || nativeReads != 1 {
				t.Fatalf("status %v, writes %d, native reads %d", err, f.writes, nativeReads)
			}
		})
	}
}

func providerMapping(id string) ([]byte, error) {
	return []byte(`{"ok":false,"error":{"code":"agent_provider_id","message":"Address it as orca_session_id:` + id + `."}}`), errors.New("provider diagnostic")
}

func TestAntigravityTerminalSyncRetriesMappingAndRequiresSinglePane(t *testing.T) {
	const id, target = "terminal_chat", "ATM 自动标题"
	for _, c := range []struct {
		name, title, expected string
		split                 bool
		wantWrite             bool
	}{
		{"delayed mapping", "agy", "agy", false, true},
		{"manual title", "my work", "agy", false, false},
		{"split tab", "agy", "agy", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			title, probes, writes := c.title, 0, 0
			run := func(_ context.Context, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(args[len(args)-3:], []string{"--environment", "local", "--json"}) {
					t.Fatalf("wrong CLI scope: %v", args)
				}
				var result any
				switch args[0] + " " + args[1] {
				case "agent show":
					if args[2] == nativeFixture {
						probes++
						if probes == 1 {
							return nil, context.DeadlineExceeded
						}
						return providerMapping(id)
					}
					at := place{ID: id, Agent: "antigravity", Kind: "terminal", HostID: "local", PaneKey: "tab:leaf", Title: &title}
					result = map[string]any{"orcaSessionId": id, "kind": at.Kind, "places": []place{at}}
				case "agent list":
					result = map[string]any{"truncated": false, "agents": []any{}}
				case "terminal list":
					pane := map[string]any{"type": "terminal", "handle": "local-handle", "leafId": "leaf", "title": "agy"}
					var panes any = pane
					if c.split {
						panes = map[string]any{"type": "split", "children": []any{pane, pane}}
					}
					result = map[string]any{
						"terminals":     []terminalEntry{{Handle: "local-handle", HostID: "local", TabID: "tab", LeafID: "leaf", Identity: "antigravity"}},
						"visualLayouts": []any{map[string]any{"tabs": []any{map[string]any{"tabId": "tab", "title": title, "panes": panes}}}},
					}
				case "terminal rename":
					if !reflect.DeepEqual(args[2:6], []string{"--terminal", "local-handle", "--title", target}) {
						t.Fatalf("wrong terminal rename: %v", args)
					}
					title, writes = target, writes+1
				default:
					t.Fatalf("unexpected command: %v", args)
				}
				return json.Marshal(map[string]any{"ok": true, "result": result})
			}
			read := func(context.Context, string, string, string) (string, error) { return target, nil }
			err := syncVerified(context.Background(), run, id, c.expected, target, "antigravity", nativeFixture, nativeFixture, read)
			if c.wantWrite && (err != nil || probes != 2 || writes != 1 || title != target) {
				t.Fatalf("status %v, mapping probes %d, rename calls %d", err, probes, writes)
			}
			if !c.wantWrite && (!errors.Is(err, ErrSkipped) || writes != 0 || title != c.title) {
				t.Fatalf("protected terminal changed: %v, %d writes", err, writes)
			}
		})
	}
}

func syncVerified(ctx context.Context, run runCLI, id, expected, title, agent, session, handle string, reader NativeTitleReader) error {
	return syncWithChecks(ctx, run, id, expected, title, agent, session, handle, syncChecks{
		read: reader, origin: databaseOrigin,
	})
}
