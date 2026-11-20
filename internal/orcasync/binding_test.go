package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
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
			if _, got := discover(context.Background(), run, nil, "codex", nativeFixture, nativeFixture, "real_chat", "", []time.Duration{0}); got != c.approved {
				t.Fatalf("approved = %v", got)
			}
			for _, agent := range []string{"external", "pi", "claude"} {
				if _, ok := discover(context.Background(), run, nil, agent, nativeFixture, "other-handle", "real_chat", "", []time.Duration{0}); ok {
					t.Fatalf("unverified %s handle was accepted", agent)
				}
			}
		})
	}
}
