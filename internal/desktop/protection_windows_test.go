package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func TestUnlockDoesNotReleaseWhenTitleReadFails(t *testing.T) {
	target := engine.ProtectionTarget{Agent: "antigravity", SessionID: "native", OrcaID: "orca", NativeHandle: "native", Baseline: "Protected"}
	readErr := errors.New("binding or native read failed")
	service := Service{backend: Backend{
		Engine: func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method != "records.protectionTarget" {
				t.Fatalf("failed read triggered %s", method)
			}
			return target, nil
		},
		ReadProtectionTitles: func(_ context.Context, agent, session, handle, id string) (string, *string, error) {
			if agent != target.Agent || session != target.SessionID || handle != target.NativeHandle || id != target.OrcaID {
				t.Fatal("unexpected target")
			}
			return "", nil, readErr
		},
	}}
	if _, err := service.Call(t.Context(), "records.unlock", `{"id":"record"}`); !errors.Is(err, readErr) {
		t.Fatalf("unlock result: %v", err)
	}
}
