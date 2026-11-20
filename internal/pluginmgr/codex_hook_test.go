package pluginmgr

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCodexHookProofRequiresCurrentBuildAndRegistration(t *testing.T) {
	m, hosts := testManager(t)
	m.SetBuildVersion("0.4.1")
	enable := func() {
		t.Helper()
		if item, err := invoke(t, m, "enable", request{ID: "codex"}); err != nil || item.Status != "action_required" {
			t.Fatalf("fresh registration: %+v %v", item, err)
		}
	}
	current := func() record {
		t.Helper()
		state, err := m.load()
		if err != nil {
			t.Fatal(err)
		}
		return state.Items["codex"]
	}
	status := func(want string) {
		t.Helper()
		item := Item{Status: "enabled"}
		m.describeEnabled("codex", &item, current())
		if item.Status != want {
			t.Fatalf("status: %+v, want %s", item, want)
		}
	}
	observe := func(token string) error {
		wire, err := json.Marshal(map[string]string{"registration": token})
		if err != nil {
			t.Fatal(err)
		}
		_, err = m.ObserveCodexHook(t.Context(), wire)
		return err
	}
	enable()
	old, _ := m.codexHookProof(current())
	if err := observe(old); err != nil {
		t.Fatal(err)
	}
	status("enabled")
	m.SetBuildVersion("0.4.2")
	status("action_required")
	if err := observe(old); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, m, "disable", request{ID: "codex"}); err != nil {
		t.Fatal(err)
	}
	enable()
	if err := observe(old); err == nil {
		t.Fatal("stale command was accepted after registration changed")
	}
	now, _ := m.codexHookProof(current())
	if err := observe(now); err != nil {
		t.Fatal(err)
	}
	status("enabled")
	writeFixture(t, filepath.Join(hosts["codex"], "hooks.json"), "{}")
	status("action_required")
	if err := observe(now); err == nil {
		t.Fatal("broken registration was accepted")
	}
}
