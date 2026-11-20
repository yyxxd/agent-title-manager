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
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
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
			Enabled:     func(_ context.Context, id string) (bool, error) { return enabled.Load() && id == "codex", nil },
			OrcaEnabled: func(context.Context, string) bool { return true },
			ResolveTitleOrigin: func(ctx context.Context, agent, session, handle, orca string) (orcasync.Binding, bool) {
				if agent != "codex" || session != "session" || handle != "native" || orca != "orca" {
					t.Error("incorrect binding identity")
				}
				close(known)
				select {
				case <-release:
					return orcasync.Binding{ID: "orca", Handle: "native", Title: "宿主读取的标题", Origin: "manual"}, true
				case <-ctx.Done():
					return orcasync.Binding{}, false
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
	fields := json.RawMessage(`{"agent":"codex","sessionId":"session","revision":"revision","turns":[],"hostSessionHint":"orca","nativeHandle":"native","orcaTitleOrigin":"auto"}`)
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
		Enabled:     func(context.Context, string) (bool, error) { return true, nil },
		OrcaEnabled: func(context.Context, string) bool { return true },
		ResolveTitleOrigin: func(context.Context, string, string, string, string) (orcasync.Binding, bool) {
			t.Fatal("community plugin was allowed to request binding verification")
			return orcasync.Binding{}, false
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
	late := startLateBindings(ctx, backend)
	defer late.stop()
	if err := event.forward(ctx, backend, late); err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"agent":"community"}` {
		t.Fatalf("Orca fields leaked: %s", output)
	}
}

func TestOrcaDisabledDiscardsNativeOnlyAndNeverResolvesBinding(t *testing.T) {
	ctx := t.Context()
	calls := 0
	backend := Backend{
		Enabled:     func(context.Context, string) (bool, error) { return true, nil },
		OrcaEnabled: func(context.Context, string) bool { return false },
		ResolveTitleOrigin: func(context.Context, string, string, string, string) (orcasync.Binding, bool) {
			t.Error("disabled integration was called from ingress")
			return orcasync.Binding{}, false
		},
		Engine: func(_ context.Context, _ string, params json.RawMessage) (any, error) {
			calls++
			if string(params) != `{"agent":"codex","revision":"turn","sessionId":"native","turns":[]}` {
				t.Errorf("unexpected native submission: %s", params)
			}
			return nil, nil
		},
	}
	svc := Service{backend: backend, ingress: startIngress(ctx, backend)}
	defer svc.ingress.stop()
	nativeOnly := json.RawMessage(`{"agent":"codex","sessionId":"native","revision":"start","nativeTitleOnly":true,"hostSessionHint":"chat","currentTitle":"existing"}`)
	if _, err := svc.pluginEvent(ctx, "event.submit", nativeOnly); err != nil || calls != 0 {
		t.Fatalf("native-only event should be ignored: %v, %d engine calls", err, calls)
	}
	submit := json.RawMessage(`{"agent":"codex","sessionId":"native","revision":"turn","turns":[],"hostSessionHint":"chat","orcaSessionId":"untrusted"}`)
	if _, err := svc.pluginEvent(ctx, "event.submit", submit); err != nil || calls != 1 {
		t.Fatalf("ordinary native event: %v, %d engine calls", err, calls)
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
		if fieldString(fields, "orcaNativeHandle") != "native" || fieldString(fields, "orcaTitleOrigin") != "manual" ||
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
