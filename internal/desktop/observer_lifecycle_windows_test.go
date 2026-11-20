package desktop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
)

func TestObserversFollowRegistrySwitchesAndShutdown(t *testing.T) {
	manager := pluginmgr.New(t.TempDir(), "")
	var agent atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, stopped, done := make(chan string, 8), make(chan string, 8), make(chan struct{})
	run := func(ctx context.Context, key string) error {
		started <- key
		<-ctx.Done()
		stopped <- key
		return nil
	}
	go func() {
		defer close(done)
		RunObservers(ctx, manager.Changes,
			Observer{Name: "desktop", Select: func(context.Context) (string, error) {
				if agent.Load() {
					return "desktop", nil
				}
				return "", nil
			}, Run: run},
			Observer{Name: "chat", Select: func(ctx context.Context) (string, error) {
				active, err := manager.IntegrationEnabled(ctx, pluginmgr.OrcaIntegration)
				if active && agent.Load() {
					return "chat", nil
				}
				return "", err
			}, Run: run})
	}()
	toggle := func(method string) {
		t.Helper()
		if _, err := manager.Handle(ctx, "plugins."+method, []byte(`{"id":"orca"}`)); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(channel <-chan string, key string) {
		t.Helper()
		select {
		case got := <-channel:
			if got != key {
				t.Fatalf("observer %s, want %s", got, key)
			}
		case <-time.After(time.Second):
			t.Fatalf("observer %s did not respond to switch", key)
		}
	}
	agent.Store(true)
	toggle("disable")
	expect(started, "desktop")
	toggle("enable")
	expect(started, "chat")
	toggle("disable")
	expect(stopped, "chat")
	toggle("enable")
	expect(started, "chat")
	cancel()
	await(t, done)
	if len(stopped) != 2 {
		t.Fatalf("shutdown left an observer running: %d stopped", len(stopped))
	}
}
