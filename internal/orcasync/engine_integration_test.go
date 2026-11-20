package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
)

func TestEngineRetryAndAdoptUseLivePiTitle(t *testing.T) {
	for _, action := range []string{"sync.retry", "sync.adopt"} {
		for _, changed := range []bool{false, true} {
			name := action + "/unchanged"
			if changed {
				name = action + "/manual-change"
			}
			t.Run(name, func(t *testing.T) { checkEngineSync(t, action, changed) })
		}
	}
}

func checkEngineSync(t *testing.T, action string, changed bool) {
	const original, target, manual = "Orca 原名 X", "规范标题", "手动标题 B"
	path, write := piTitleFile(t, target)
	var fixtureLock sync.Mutex
	fixture := &cliFixture{t: t, agent: "pi", title: original, write: true}
	var temporarilyUnreadable atomic.Bool
	temporarilyUnreadable.Store(action == "sync.retry")
	callback := func(ctx context.Context, raw, expected, candidate string) error {
		fixtureLock.Lock()
		defer fixtureLock.Unlock()
		return verifiedPiCallback(ctx, fixture, path, write, &temporarilyUnreadable, raw, expected, candidate)
	}
	core, err := engine.Open(t.TempDir(), callback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	origin, first := "manual", "protected"
	if action == "sync.retry" {
		origin, first = "auto", "failed"
	}
	engineRPC(t, core, "event.submit", map[string]any{
		"agent": "pi", "sessionId": nativeFixture, "revision": "confirmed", "currentTitle": target,
		"nativeTitleOnly": true, "orcaSessionId": "codex_chat", "orcaTitle": original, "orcaTitleOrigin": origin,
		"orcaNativeHandle": path, "turns": []any{},
	}, nil)
	recordID := waitForSyncRecord(t, core, first)
	if changed {
		write(manual)
	}
	temporarilyUnreadable.Store(false)
	var accepted struct{ Accepted bool }
	engineRPC(t, core, action, map[string]string{"id": recordID}, &accepted)
	if !accepted.Accepted {
		t.Fatal("retry or adoption did not queue the guarded write")
	}
	if !changed {
		waitForSyncRecord(t, core, "synced")
		assertPiSyncTitles(t, &fixtureLock, fixture, path, target, target, 1)
		return
	}
	waitForSyncRecord(t, core, "stale")
	assertPiSyncTitles(t, &fixtureLock, fixture, path, original, manual, 0)
}

func verifiedPiCallback(ctx context.Context, fixture *cliFixture, path string, write func(string), unreadable *atomic.Bool, raw, expected, candidate string) error {
	target, ok := engine.SyncTargetFromContext(ctx)
	if !ok || target.ExpectedTitle != candidate {
		return ErrNativePending
	}
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "agent" && args[1] == "show" && args[2] == path {
			return []byte(`{"ok":false,"error":{"code":"agent_provider_id","message":"Address it as orca_session_id:codex_chat."}}`), errors.New("provider diagnostic")
		}
		if args[0] == "chat" && args[1] == "rename" {
			write(args[5])
		}
		return fixture.run(ctx, args...)
	}
	reader := func(ctx context.Context, _, id, handle string) (string, error) {
		if unreadable.Load() {
			return "", ErrNativePending
		}
		return pluginmgr.ReadPiTitle(ctx, id, handle)
	}
	err := syncWithChecks(ctx, run, raw, expected, candidate, target.Agent, target.SessionID, target.NativeHandle, syncChecks{read: reader, validate: (&pluginmgr.Manager{}).ValidateNativeHandle, origin: databaseOrigin})
	if errors.Is(err, ErrNativeStale) {
		return engine.ErrSyncStale
	}
	return err
}

func assertPiSyncTitles(t *testing.T, lock *sync.Mutex, fixture *cliFixture, path, orca, native string, writes int) {
	t.Helper()
	lock.Lock()
	defer lock.Unlock()
	current, err := pluginmgr.ReadPiTitle(context.Background(), nativeFixture, path)
	if err != nil || current != native || fixture.title != orca || fixture.writes != writes {
		t.Fatalf("Orca %q, native %q, writes %d, error %v", fixture.title, current, fixture.writes, err)
	}
}

func engineRPC(t *testing.T, e *engine.Engine, method string, value, target any) {
	t.Helper()
	params, _ := json.Marshal(value)
	result, err := e.Handle(context.Background(), method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if target != nil {
		wire, _ := json.Marshal(result)
		if err := json.Unmarshal(wire, target); err != nil {
			t.Fatalf("%s result: %v", method, err)
		}
	}
}

func waitForSyncRecord(t *testing.T, e *engine.Engine, status string) string {
	t.Helper()
	var result struct {
		Items []struct{ ID, SyncStatus string }
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		engineRPC(t, e, "records.list", map[string]any{}, &result)
		if len(result.Items) != 0 && result.Items[0].SyncStatus == status {
			return result.Items[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wanted status %s; got %+v", status, result.Items)
	return ""
}
