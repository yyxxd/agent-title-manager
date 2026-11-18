package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/yyxxd/agent-title-manager/internal/ipc"
)

func TestBridgeChecksEnabledAgentAndQueuesBinding(t *testing.T) {
	var enabled atomic.Bool
	known := make(chan struct{})
	release := make(chan struct{})
	received := make(chan json.RawMessage, 2)
	service := NewService(".bridge-"+time.Now().Format("150405.000000000"), func() (Backend, error) {
		return Backend{
			Engine: func(_ context.Context, method string, fields json.RawMessage) (any, error) {
				if method == "event.submit" {
					received <- fields
				}
				return nil, nil
			},
			Enabled: func(_ context.Context, id string) (bool, error) { return enabled.Load() && id == "codex", nil },
			ResolveTitleOrigin: func(ctx context.Context, agent, session, handle, orca string) (string, string, bool) {
				if agent != "codex" || session != "session" || handle != "native" || orca != "orca" {
					t.Error("incorrect binding identity")
				}
				close(known)
				select {
				case <-release:
					return "宿主读取的标题", "manual", true
				case <-ctx.Done():
					return "", "unknown", false
				}
			},
			Close: func() error { return nil },
		}, nil
	})
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.ServiceShutdown(); err != nil {
			t.Error(err)
		}
	})
	fields := json.RawMessage(`{"agent":"codex","sessionId":"session","revision":"revision","turns":[],"orcaSessionId":"orca","orcaNativeHandle":"native","orcaTitleOrigin":"auto"}`)
	if err := callBridge(service, "event.submit", fields); err == nil {
		t.Fatal("accepted disabled agent")
	}
	enabled.Store(true)
	if err := callBridge(service, "event.submit", fields); err != nil {
		t.Fatal(err)
	}
	await(t, known)
	if err := callBridge(service, "title.claim", fields); err != nil {
		t.Fatal(err)
	}
	if err := callBridge(service, "event.submit", fields); err != nil {
		t.Fatal(err)
	}
	close(release)
	assertBinding(t, received)
	enabled.Store(false)
	if err := callBridge(service, "title.ack", fields); err == nil {
		t.Fatal("accepted disabled agent ack")
	}
}

func TestCommunitySubmissionCannotSupplyOrcaBinding(t *testing.T) {
	ctx := t.Context()
	var output json.RawMessage
	backend := Backend{
		Enabled: func(context.Context, string) (bool, error) { return true, nil },
		ResolveTitleOrigin: func(context.Context, string, string, string, string) (string, string, bool) {
			t.Fatal("community plugin was allowed to request binding verification")
			return "", "unknown", false
		},
		Engine: func(_ context.Context, _ string, params json.RawMessage) (any, error) {
			output = params
			return nil, nil
		},
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"agent":"community","orcaSessionId":"x","orcaNativeHandle":"y","OrcaTitle":"z","orcaTitleOrigin":"auto"}`), &fields); err != nil {
		t.Fatal(err)
	}
	event := pendingEvent{key: eventKey{agent: "community"}, fields: fields}
	if err := event.forward(ctx, backend); err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"agent":"community"}` {
		t.Fatalf("Orca fields leaked: %s", output)
	}
}

func assertBinding(t *testing.T, received <-chan json.RawMessage) {
	t.Helper()
	select {
	case raw := <-received:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["orcaNativeHandle"] != nil || fieldString(fields, "orcaTitleOrigin") != "manual" ||
			fieldString(fields, "orcaTitle") != "宿主读取的标题" || fieldString(fields, "orcaSessionId") != "orca" {
			t.Fatalf("unsanitized binding: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("queued event not delivered")
	}
}

func callBridge(service *Service, method string, fields json.RawMessage) error {
	response, err := ipc.Call(context.Background(), service.scope, ipc.Request{V: 1, Method: method, Params: fields})
	if err != nil {
		return err
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	return nil
}

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background check did not start")
	}
}
