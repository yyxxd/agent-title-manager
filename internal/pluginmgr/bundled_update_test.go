package pluginmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnabledBuiltinDiscoversAndAppliesBundledUpdate(t *testing.T) {
	m, host := testManager(t)
	home := host["codex"]
	config := filepath.Join(home, "config.toml")
	const original = "[plugins.\"oil-codex-title@oil-codex-title-local\"]\nenabled = true\n"
	writeFixture(t, config, original)
	hooks := filepath.Join(home, "hooks.json")
	mustInvoke(t, m, "enable", request{ID: "codex", Takeover: true})
	v1Hooks := readFixture(t, hooks)
	packageFixture(t, filepath.Join(m.bundled, "codex"), "codex", "codex", "2.0.0")
	listed, err := m.Handle(context.Background(), "plugins.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	items := listed.(struct {
		Items []Item `json:"items"`
	}).Items
	if list := items[0]; !list.Enabled || list.Version != "1.0.0" || list.AvailableVersion != "2.0.0" {
		t.Fatalf("upgrade is not visible: %+v", list)
	}
	got := mustInvoke(t, m, "update", request{ID: "codex", Source: "bundled"})
	if !got.Enabled || !got.CanRestore || got.Version != "2.0.0" || got.AvailableVersion != "" || got.Status != "action_required" {
		t.Fatalf("unexpected upgraded state: %+v", got)
	}
	if content := readFixture(t, hooks); content == v1Hooks || strings.Count(content, "test-entry.mjs") != 2 {
		t.Fatal("upgrade did not replace both old hook registrations")
	}
	mustInvoke(t, m, "restore", request{ID: "codex"})
	if readFixture(t, config) != original {
		t.Fatal("upgrade lost legacy takeover information")
	}
	if _, err := os.Stat(hooks); !os.IsNotExist(err) {
		t.Fatal("upgrade left manager-owned hooks after recovery")
	}
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
