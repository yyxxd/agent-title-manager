package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	wire, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}

func packageFixture(t *testing.T, dir, id, host, version string) {
	t.Helper()
	m := Manifest{1, id, "测试适配器", version, "agent", host, "test-entry.mjs"}
	wire, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, manifestName), string(wire))
	writeFixture(t, filepath.Join(dir, m.Entry), `throw new Error("安装时不能执行社区入口");`)
}

func testManager(t *testing.T) (*Manager, map[string]string) {
	t.Helper()
	root := t.TempDir()
	bundle := filepath.Join(root, "release with spaces", "plugins")
	writeFixture(t, filepath.Join(bundle, "node.exe"), "never invoked")
	writeFixture(t, filepath.Join(filepath.Dir(bundle), "atm-bridge.exe"), "never invoked")
	hostPaths := map[string]string{}
	hostEnvs := map[string]string{"codex": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR", "pi": "PI_CODING_AGENT_DIR", "antigravity": "ANTIGRAVITY_CONFIG_DIR"}
	for id, env := range hostEnvs {
		home := filepath.Join(root, id+" config")
		hostPaths[id] = home
		t.Setenv(env, home)
		packageFixture(t, filepath.Join(bundle, id), id, id, "1.0.0")
	}
	return New(filepath.Join(root, "application data"), bundle), hostPaths
}

func invoke(t *testing.T, m *Manager, verb string, req request) (Item, error) {
	t.Helper()
	wire, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Handle(context.Background(), "plugins."+verb, wire)
	if err != nil {
		return Item{}, err
	}
	item, ok := out.(Item)
	if !ok {
		t.Fatalf("invalid result %#v", out)
	}
	return item, nil
}

func mustInvoke(t *testing.T, m *Manager, verb string, req request) Item {
	t.Helper()
	item, err := invoke(t, m, verb, req)
	if err != nil {
		t.Fatalf("%s: %v", verb, err)
	}
	return item
}

func TestListAndSyncDefaultDisabled(t *testing.T) {
	m, _ := testManager(t)
	ctx := context.Background()
	out, err := m.Handle(ctx, "plugins.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out.(struct {
		Items      []Item            `json:"items"`
		AutoUpdate *AutoUpdateResult `json:"autoUpdate,omitempty"`
	}).Items
	for i, expected := range []string{"codex", "claude", "pi", "antigravity", "orca"} {
		if got[i].ID != expected || got[i].Enabled || got[i].Status != "disabled" {
			t.Fatalf("unexpected builtin %+v", got[i])
		}
	}
	if _, err := os.Stat(m.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only list created plugin state: %v", err)
	}
	mustInvoke(t, m, "enable", request{ID: "orca"})
	if enabled, _ := m.Enabled(ctx, "orca"); enabled {
		t.Fatal("Orca sync may not register a bridge agent")
	}
	if enabled, err := m.IntegrationEnabled(ctx, "orca"); !enabled || err != nil {
		t.Fatalf("missing Orca sync flag %v, %v", enabled, err)
	}
	mustInvoke(t, m, "disable", request{ID: "orca"})
	if enabled, err := m.IntegrationEnabled(ctx, "orca"); enabled || err != nil {
		t.Fatalf("stale Orca sync flag %v, %v", enabled, err)
	}
}

func TestCommunityInstallEnableUpdate(t *testing.T) {
	m, _ := testManager(t)
	ctx := context.Background()
	v1, v2 := t.TempDir(), t.TempDir()
	packageFixture(t, v1, "external-test", "external", "1.0.0")
	packageFixture(t, v2, "external-test", "external", "1.1.0")
	req := request{ID: "external-test"}
	install := request{Source: "local", Path: v1}
	update := request{ID: req.ID, Source: "local", Path: v2}
	if got := mustInvoke(t, m, "install", install); got.Enabled || got.Version != "1.0.0" || got.Kind != "community" {
		t.Fatalf("unexpected install %+v", got)
	}
	mustInvoke(t, m, "enable", req)
	if got, err := m.Enabled(ctx, req.ID); err != nil || !got {
		t.Fatalf("bridge gate %v, %v", got, err)
	}
	if _, err := invoke(t, m, "update", update); err == nil {
		t.Fatal("active plugin updated")
	}
	mustInvoke(t, m, "disable", req)
	if got := mustInvoke(t, m, "update", update); got.Version != "1.1.0" || got.Enabled {
		t.Fatalf("unexpected update %+v", got)
	}
	if got, err := m.Enabled(ctx, req.ID); err != nil || got {
		t.Fatalf("disabled plugin available through bridge %v, %v", got, err)
	}
	if _, err := m.Handle(ctx, "plugins.restore", json.RawMessage(`{"id":"external-test"}`)); err == nil {
		t.Fatal("plugins.restore should be rejected")
	}
}

func TestCodexLifecyclePreservesUnrelatedHooks(t *testing.T) {
	m, host := testManager(t)
	dir := host["codex"]
	hooksFile := filepath.Join(dir, "hooks.json")
	const hooks = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"another-hook"}]}]}}`
	writeFixture(t, hooksFile, hooks)
	got := mustInvoke(t, m, "enable", request{ID: "codex"})
	if !got.Enabled || got.Status != "action_required" {
		t.Fatalf("unexpected Codex status %+v", got)
	}
	for _, s := range []string{"SessionStart", "another-hook", "test-entry.mjs", "\"async\": true"} {
		if !strings.Contains(readFixture(t, hooksFile), s) {
			t.Fatalf("missing expected Codex registration %s", s)
		}
	}
	mustInvoke(t, m, "disable", request{ID: "codex"})
	gotHooks := readFixture(t, hooksFile)
	if strings.Contains(gotHooks, "test-entry.mjs") || !strings.Contains(gotHooks, "another-hook") {
		t.Fatalf("disable did not cleanly remove ATM registration while keeping user hooks: %s", gotHooks)
	}
}

func TestClaudeLifecyclePreservesUserSettingsAndHooks(t *testing.T) {
	m, host := testManager(t)
	settings := filepath.Join(host["claude"], "settings.json")
	wire, err := json.Marshal(map[string]any{
		"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{
			"hooks": []map[string]string{{"type": "command", "command": "another-hook"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settings, string(wire))
	mustInvoke(t, m, "enable", request{ID: "claude"})
	enabled := readFixture(t, settings)
	if !strings.Contains(enabled, "another-hook") ||
		!strings.Contains(enabled, "\"timeout\": 3") || !strings.Contains(enabled, "\"Stop\"") {
		t.Fatal("Claude event registration is wrong")
	}
	writeFixture(t, settings, strings.Replace(enabled, `"hooks": {`, `"model": "user-edit", "hooks": {`, 1))
	mustInvoke(t, m, "disable", request{ID: "claude"})
	disabled := readFixture(t, settings)
	for _, marker := range []string{"another-hook", "user-edit"} {
		if !strings.Contains(disabled, marker) || strings.Contains(disabled, "test-entry.mjs") {
			t.Fatal("Claude disable did not preserve user configurations")
		}
	}
}

func TestPiLifecyclePreservesUnrelatedExtensions(t *testing.T) {
	m, host := testManager(t)
	home := host["pi"]
	settings := filepath.Join(home, "settings.json")
	const original = `{"extensions":["extensions/other.js"]}`
	writeFixture(t, settings, original)
	mustInvoke(t, m, "enable", request{ID: "pi"})
	if got := readFixture(t, settings); !strings.Contains(got, "other.js") || !strings.Contains(got, "test-entry.mjs") {
		t.Fatal("Pi registration did not preserve other extensions")
	}
	mustInvoke(t, m, "disable", request{ID: "pi"})
	got := readFixture(t, settings)
	if strings.Contains(got, "test-entry.mjs") || !strings.Contains(got, "extensions/other.js") {
		t.Fatalf("Pi disable did not preserve unrelated extensions: %s", got)
	}
}
