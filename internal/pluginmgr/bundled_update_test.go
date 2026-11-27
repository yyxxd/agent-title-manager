package pluginmgr

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnabledBuiltinDiscoversAndAppliesBundledUpdate(t *testing.T) {
	m, host := testManager(t)
	home := host["codex"]
	hooks := filepath.Join(home, "hooks.json")
	packageFixture(t, filepath.Join(m.bundled, "codex"), "codex", "codex", "0.1.0")
	mustInvoke(t, m, "enable", request{ID: "codex"})
	v1Dir := useOldCodexRegistration(t, m, hooks)
	writeFixture(t, hooks, strings.Replace(readFixture(t, hooks), "{", `{"userEdit":true,`, 1))
	packageFixture(t, filepath.Join(m.bundled, "codex"), "codex", "codex", "0.1.1")
	listed, err := m.Handle(context.Background(), "plugins.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(struct {
		Items      []Item            `json:"items"`
		AutoUpdate *AutoUpdateResult `json:"autoUpdate,omitempty"`
	}).Items
	if list := items[0]; !list.Enabled || list.Version != "0.1.0" || list.AvailableVersion != "0.1.1" {
		t.Fatalf("upgrade is not visible: %+v", list)
	}
	got := mustInvoke(t, m, "update", request{ID: "codex", Source: "bundled"})
	if !got.Enabled || got.Version != "0.1.1" || got.AvailableVersion != "" || got.Status != "action_required" {
		t.Fatalf("unexpected upgraded state: %+v", got)
	}
	if content := readFixture(t, hooks); strings.Contains(content, v1Dir) || strings.Count(content, "test-entry.mjs") != 2 {
		t.Fatal("upgrade did not replace both old hook registrations")
	}
	mustInvoke(t, m, "disable", request{ID: "codex"})
	if content := readFixture(t, hooks); strings.Contains(content, "test-entry.mjs") || !strings.Contains(content, `"userEdit": true`) {
		t.Fatal("disable did not preserve the user edit and remove manager-owned hooks")
	}
}

func useOldCodexRegistration(t *testing.T, m *Manager, settings string) string {
	t.Helper()
	state, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	old := state.Items["codex"]
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(m.packageDir(old.release), old.Manifest.Entry)
	old.Entry = "\"" + node + "\" \"" + script + "\""
	owned, err := registration(host{id: "codex", settings: settings}, old.Registration[0].Before, old.Entry, true)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settings, string(owned.After.Data))
	old.Registration = []change{owned}
	state.Items["codex"] = old
	if err := m.save(state); err != nil {
		t.Fatal(err)
	}
	return old.Dir
}

func TestBundledUpdateFailureKeepsOldHostRegistration(t *testing.T) {
	m, host := testManager(t)
	mustInvoke(t, m, "enable", request{ID: "claude"})
	settings := filepath.Join(host["claude"], "settings.json")
	old := readFixture(t, settings)
	dir := filepath.Join(m.bundled, "claude")
	packageFixture(t, dir, "claude", "claude", "1.1.0")
	writeFixture(t, filepath.Join(dir, runtimeName), "{}")
	if _, err := invoke(t, m, "update", request{ID: "claude", Source: "bundled"}); err == nil {
		t.Fatal("invalid bundled update was accepted")
	}
	if got := readFixture(t, settings); got != old {
		t.Fatal("upgrade failure changed old registration")
	}
	state, err := m.load()
	if err != nil || !state.Items["claude"].Enabled || state.Items["claude"].Manifest.Version != "1.0.0" || state.Pending != nil {
		t.Fatalf("upgrade failure changed valid state: %v", err)
	}
}

func TestCodexBundledUpdatePreservesTrustedCommand(t *testing.T) {
	m, host := testManager(t)
	bundle := filepath.Join(m.bundled, "codex")
	stage := func(version string) {
		packageFixture(t, bundle, "codex", "codex", version)
		manifest := filepath.Join(bundle, manifestName)
		writeFixture(t, manifest, strings.ReplaceAll(readFixture(t, manifest), "test-entry.mjs", "index.mjs"))
		writeFixture(t, filepath.Join(bundle, "index.mjs"), "")
	}
	stage("0.2.2")
	mustInvoke(t, m, "enable", request{ID: "codex"})
	hooks := filepath.Join(host["codex"], "hooks.json")
	before := readFixture(t, hooks)
	if !strings.Contains(before, "codex-hook.mjs") {
		t.Fatal("Codex hook still points at a versioned package")
	}
	stage("0.2.3")
	got := mustInvoke(t, m, "update", request{ID: "codex", Source: "bundled"})
	if got.Version != "0.2.3" || !got.Enabled || readFixture(t, hooks) != before {
		t.Fatalf("upgrading changed the trusted registration: %+v", got)
	}
	mustInvoke(t, m, "disable", request{ID: "codex"})
	if registration, err := readSettings(hooks); err != nil || registration.Exists {
		t.Fatalf("disabled Codex hook remains registered: %v", err)
	}
}

func TestBuiltinVersionsUseSemVerPrecedence(t *testing.T) {
	for _, c := range []struct {
		candidate, current string
		want               bool
	}{
		{"10.0.0", "9.1.3", true},
		{"1.0.0", "1.0.0-rc.2", true},
		{"1.0.0-rc.10", "1.0.0-rc.2", true},
		{"1.0.0-rc.1", "1.0.0-rc.beta", false},
		{"1.0.0+2", "1.0.0+1", false},
		{"1.0.0-rc.2", "1.0.0", false},
	} {
		if got := newerVersion(c.candidate, c.current); got != c.want {
			t.Errorf("%s > %s = %v", c.candidate, c.current, got)
		}
	}
}
