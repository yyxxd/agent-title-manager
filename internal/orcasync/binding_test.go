package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const nativeFixture = "12345678-1234-1234-1234-123456789abc"

func TestBindingUsesFinalDiagnosticAddress(t *testing.T) {
	for _, c := range []struct {
		name, code, final, agent, host string
		approved                       bool
	}{
		{"valid", "agent_provider_id", "real_chat.", "codex", "local", true},
		{"earlier address does not count", "agent_provider_id", "other_chat.", "codex", "local", false},
		{"invalid diagnostic", "not_found", "real_chat.", "codex", "local", false},
		{"trailing ambiguous message", "agent_provider_id", "real_chat. Extra text", "codex", "local", false},
		{"wrong agent", "agent_provider_id", "real_chat.", "claude", "local", false},
		{"remote chat", "agent_provider_id", "real_chat.", "codex", "remote", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := func(_ context.Context, args ...string) ([]byte, error) {
				if args[2] == nativeFixture {
					wire, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]string{
						"code": c.code, "message": "Address it as orca_session_id:real_chat. Address it as orca_session_id:" + c.final,
					}})
					return wire, errors.New("CLI diagnostic")
				}
				at := place{ID: "real_chat", Kind: "chat", HostID: c.host, Agent: c.agent}
				return json.Marshal(map[string]any{"ok": true, "result": map[string]any{
					"orcaSessionId": at.ID, "kind": "chat", "places": []place{at},
				}})
			}
			if got := approveBinding(context.Background(), run, "codex", nativeFixture, nativeFixture, "real_chat"); got != c.approved {
				t.Fatalf("approved = %v", got)
			}
			for _, agent := range []string{"external", "pi", "claude"} {
				if approveBinding(context.Background(), run, agent, nativeFixture, "other-handle", "real_chat") {
					t.Fatalf("unverified %s handle was accepted", agent)
				}
			}
		})
	}
}

func TestPiBindingRequiresSessionFileOfSameNativeID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.Mkdir(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	name := "2026-11-18T01-02-03_" + nativeFixture + ".jsonl"
	path := filepath.Join(sessions, name)
	put := func(path, head string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(head+"\n{malformed second line"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(path, `{"type":"session","id":"`+nativeFixture+`"}`)
	if !validPiSession(context.Background(), nativeFixture, path) {
		t.Fatal("valid header rejected")
	}
	put(path, `{"type":"session","id":"wrong"}`)
	if validPiSession(context.Background(), nativeFixture, path) {
		t.Fatal("incorrect session header accepted")
	}
	outside := filepath.Join(home, name)
	put(outside, `{"type":"session","id":"`+nativeFixture+`"}`)
	if validPiSession(context.Background(), nativeFixture, outside) {
		t.Fatal("outside file accepted")
	}
}
