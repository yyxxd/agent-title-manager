package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/orcasync"
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
	hostEnvs := map[string]string{"codex": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR", "pi": "PI_CODING_AGENT_DIR"}
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
		Items []Item `json:"items"`
	}).Items
	for i, expected := range []string{"codex", "claude", "pi", "orca"} {
		if got[i].ID != expected || got[i].Enabled || got[i].Status != "disabled" {
			t.Fatalf("unexpected builtin %+v", got[i])
		}
	}
	if err := m.Sync(ctx, "id", "old", "new"); !errors.Is(err, orcasync.ErrSkipped) {
		t.Fatalf("default sync status %v", err)
	}
	if _, err := os.Stat(m.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only list created plugin state: %v", err)
	}
	mustInvoke(t, m, "enable", request{ID: "orca"})
	if enabled, _ := m.Enabled(ctx, "orca"); enabled {
		t.Fatal("Orca sync may not register a bridge agent")
	}
	if enabled, err := m.enabled(ctx, "orca"); !enabled || err != nil {
		t.Fatalf("missing Orca sync flag %v, %v", enabled, err)
	}
	mustInvoke(t, m, "disable", request{ID: "orca"})
	if enabled, err := m.enabled(ctx, "orca"); enabled || err != nil {
		t.Fatalf("stale Orca sync flag %v, %v", enabled, err)
	}
}

func TestCommunityInstallEnableUpdateRestore(t *testing.T) {
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
	if got := mustInvoke(t, m, "update", update); got.Version != "1.1.0" || !got.CanRestore {
		t.Fatalf("unexpected update %+v", got)
	}
	if got := mustInvoke(t, m, "restore", req); got.Version != "1.0.0" || got.Enabled || got.CanRestore {
		t.Fatalf("unexpected restore %+v", got)
	}
	if got, err := m.Enabled(ctx, req.ID); err != nil || got {
		t.Fatalf("disabled plugin available through bridge %v, %v", got, err)
	}
}

func TestCodexTakeoverLeavesTrustAndOtherPluginsAlone(t *testing.T) {
	m, host := testManager(t)
	dir := host["codex"]
	config := filepath.Join(dir, "config.toml")
	const prior = "[plugins.\"oil-codex-title@oil-codex-title-local\"]\r\nenabled = true # old\r\n[plugins.\"other@official\"]\r\nenabled = true\r\n[hooks.state.\"saved:stop:0:0\"]\r\ntrusted_hash = \"keep\"\r\n"
	writeFixture(t, config, prior)
	hooksFile := filepath.Join(dir, "hooks.json")
	const hooks = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"another-hook"}]}]}}`
	writeFixture(t, hooksFile, hooks)
	if _, err := invoke(t, m, "enable", request{ID: "codex"}); err == nil || readFixture(t, config) != prior {
		t.Fatal("legacy plugin changed before approval")
	}
	got := mustInvoke(t, m, "enable", request{ID: "codex", Takeover: true})
	if !got.Enabled || !got.CanRestore || got.Status != "action_required" {
		t.Fatalf("unexpected Codex status %+v", got)
	}
	if want := strings.Replace(prior, "enabled = true # old", "enabled = false # old", 1); readFixture(t, config) != want {
		t.Fatal("unrelated Codex configuration changed")
	}
	for _, s := range []string{"SessionStart", "another-hook", "test-entry.mjs", "\"async\": true"} {
		if !strings.Contains(readFixture(t, hooksFile), s) {
			t.Fatalf("missing expected Codex registration %s", s)
		}
	}
	mustInvoke(t, m, "disable", request{ID: "codex"})
	if !strings.Contains(readFixture(t, config), "enabled = false # old") {
		t.Fatal("disable unexpectedly re-enabled an older plugin")
	}
	writeFixture(t, config, readFixture(t, config)+"# unrelated manual edit\r\n")
	mustInvoke(t, m, "restore", request{ID: "codex"})
	if readFixture(t, config) != prior+"# unrelated manual edit\r\n" || readFixture(t, hooksFile) != hooks {
		t.Fatal("restore lost unrelated settings or failed to restore legacy plugin")
	}
}

func TestClaudeTakeoverRestoresLegacyWithoutDeletingNewSettings(t *testing.T) {
	m, host := testManager(t)
	settings := filepath.Join(host["claude"], "settings.json")
	legacy := "py -3 \"" + filepath.Join(host["claude"], "session-namer", "namer.py") + "\""
	wire, err := json.Marshal(map[string]any{
		"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{
			"hooks": []map[string]string{{"type": "command", "command": legacy}, {"type": "command", "command": "another-hook"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settings, string(wire))
	mustInvoke(t, m, "enable", request{ID: "claude", Takeover: true})
	enabled := readFixture(t, settings)
	if strings.Contains(enabled, "namer.py") || !strings.Contains(enabled, "another-hook") ||
		!strings.Contains(enabled, "\"timeout\": 3") || !strings.Contains(enabled, "\"Stop\"") {
		t.Fatal("Claude hook migration or event registration is wrong")
	}
	writeFixture(t, settings, strings.Replace(enabled, `"hooks": {`, `"model": "user-edit", "hooks": {`, 1))
	mustInvoke(t, m, "restore", request{ID: "claude"})
	restored := readFixture(t, settings)
	for _, marker := range []string{"namer.py", "another-hook", "user-edit"} {
		if !strings.Contains(restored, marker) || strings.Contains(restored, "test-entry.mjs") {
			t.Fatal("Claude restore did not preserve affected configurations")
		}
	}
}

func TestPiTakeoverKeepsExtensionDataAndUnrelatedExtensions(t *testing.T) {
	m, host := testManager(t)
	home := host["pi"]
	ext := filepath.Join(home, "extensions")
	old := filepath.Join(ext, "auto-name")
	archive := filepath.Join(home, ".atm-disabled-auto-name")
	const state = `{"history":"preserve"}`
	writeFixture(t, filepath.Join(old, "index.js"), "import {x} from './core.js'; pi.registerCommand('auto-name', {});")
	writeFixture(t, filepath.Join(old, "state.json"), state)
	writeFixture(t, filepath.Join(ext, "other.js"), "other extension")
	settings := filepath.Join(home, "settings.json")
	const original = `{"extensions":["extensions/auto-name/index.js","extensions/other.js"]}`
	writeFixture(t, settings, original)
	mustInvoke(t, m, "enable", request{ID: "pi", Takeover: true})
	if _, err := os.Stat(old); !os.IsNotExist(err) || readFixture(t, filepath.Join(archive, "state.json")) != state {
		t.Fatal("legacy Pi extension was not moved with its data")
	}
	if got := readFixture(t, settings); strings.Contains(got, "auto-name") ||
		!strings.Contains(got, "other.js") || !strings.Contains(got, "test-entry.mjs") {
		t.Fatal("Pi registration did not preserve other extensions")
	}
	mustInvoke(t, m, "restore", request{ID: "pi"})
	if readFixture(t, settings) != original || readFixture(t, filepath.Join(old, "state.json")) != state {
		t.Fatal("legacy Pi files and settings did not restore")
	}
}
