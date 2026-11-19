package pluginmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallRequiresDisablingManagedHostsAndRollingBackPending(t *testing.T) {
	m, _ := testManager(t)
	ctx := context.Background()
	if err := m.UninstallReady(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall check wrote state: %v", err)
	}
	mustInvoke(t, m, "enable", request{ID: "codex"})
	if err := m.UninstallReady(ctx); err == nil {
		t.Fatal("active plugin did not block uninstall")
	}
	mustInvoke(t, m, "disable", request{ID: "codex"})
	if err := m.UninstallReady(ctx); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, m.stateFile(), "{malformed state")
	if err := m.UninstallReady(ctx); err == nil {
		t.Fatal("unreadable plugin state did not block uninstall")
	}
}

func TestChangeTransactionRollsBackEarlierWriteOnConflict(t *testing.T) {
	m, _ := testManager(t)
	first, second := filepath.Join(m.root, "first.json"), filepath.Join(m.root, "second.json")
	writeFixture(t, first, "first")
	writeFixture(t, second, "concurrent edit")
	tx := transaction{ID: "codex", Changes: []change{
		{File: first, Before: snapshot{true, []byte("first")}, After: snapshot{true, []byte("new")}},
		{File: second, Before: snapshot{true, []byte("stale")}, After: snapshot{true, []byte("unsafe")}},
	}}
	if err := m.execute(registry{Items: map[string]record{}}, tx); err == nil {
		t.Fatal("conflicting changes were accepted")
	}
	if readFixture(t, first) != "first" || readFixture(t, second) != "concurrent edit" {
		t.Fatal("rollback did not preserve original settings")
	}
	state, err := m.load()
	if err != nil || state.Pending != nil || len(state.Items) != 0 {
		t.Fatalf("unexpected state after rollback %+v, %v", state, err)
	}
}

func TestInterruptedManagementRollsBackOnlyAttemptedActionsOnDisable(t *testing.T) {
	m, _ := testManager(t)
	ctx := context.Background()
	first, second := filepath.Join(m.root, "first.json"), filepath.Join(m.root, "second.json")
	writeFixture(t, second, "untouched edit")
	c := change{File: first, After: snapshot{true, []byte("half applied")}}
	journal := transaction{ID: "pi", Attempted: 1, Changes: []change{c, {File: second}}}
	if err := m.save(registry{Items: map[string]record{}, Pending: &journal}); err != nil {
		t.Fatal(err)
	}
	if err := c.apply(); err != nil {
		t.Fatal(err)
	}
	state, err := m.load()
	if err != nil || m.item(state, "pi").Status != "recovery_required" {
		t.Fatalf("unfinished changes were not reported: %v", err)
	}
	if err := m.UninstallReady(ctx); err == nil {
		t.Fatal("pending transaction did not block uninstall")
	}
	if _, err := invoke(t, m, "enable", request{ID: "codex"}); err == nil {
		t.Fatal("unrelated enable should be blocked while pending transaction exists")
	}
	got := mustInvoke(t, m, "disable", request{ID: "pi"})
	if got.Status != "disabled" || got.Enabled {
		t.Fatalf("unexpected status after pending rollback: %+v", got)
	}
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) || readFixture(t, second) != "untouched edit" {
		t.Fatal("recovery changed an unattempted host action")
	}
	if err := m.UninstallReady(ctx); err != nil {
		t.Fatalf("uninstall should succeed after pending rollback: %v", err)
	}
}
