package pluginmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func codexLegacyFixture(home string) string {
	file := filepath.Join(home, "plugins", "cache", "oil-codex-title-local", "oil-codex-title", "1.0.0", "scripts", "oil_codex_title.py")
	return `py -3 "` + file + `"`
}

func hookFixture(t *testing.T, file string, handler map[string]string) {
	t.Helper()
	wire, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"Stop": []any{map[string]any{"hooks": []any{handler}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, file, string(wire))
}

func TestReviewCodexTakeoverKeepsRealWindowsCommand(t *testing.T) {
	m, host := testManager(t)
	h := host["codex"]
	file := filepath.Join(h, "hooks.json")
	hookFixture(t, file, map[string]string{
		"type": "command", "command": codexLegacyFixture(h), "commandWindows": "echo unrelated-windows-hook",
	})
	mustInvoke(t, m, "enable", request{ID: "codex", Takeover: true})
	if cfg := readFixture(t, file); !strings.Contains(cfg, "echo unrelated-windows-hook") || !strings.Contains(cfg, "oil_codex_title.py") {
		t.Fatal("takeover changed a handler whose effective Windows command was unrelated")
	}
}

func TestCodexTakeoverPreservesOtherPlatformsAndAddedHandler(t *testing.T) {
	m, host := testManager(t)
	h := host["codex"]
	file := filepath.Join(h, "hooks.json")
	hookFixture(t, file, map[string]string{
		"type": "command", "command": "echo non-windows-hook", "commandWindows": codexLegacyFixture(h),
	})
	mustInvoke(t, m, "enable", request{ID: "codex", Takeover: true})
	mustInvoke(t, m, "disable", request{ID: "codex"})
	disabled := readFixture(t, file)
	if !strings.Contains(disabled, "echo non-windows-hook") || strings.Contains(disabled, "oil_codex_title.py") ||
		!strings.Contains(disabled, inactiveWindowsCommand) {
		t.Fatal("takeover did not isolate the Windows command")
	}
	writeFixture(t, file, strings.Replace(disabled, `"hooks": [`, `"hooks": [{"type":"command","command":"echo user-added"},`, 1))
	mustInvoke(t, m, "restore", request{ID: "codex"})
	after := readFixture(t, file)
	for _, want := range []string{"echo non-windows-hook", "oil_codex_title.py", "echo user-added"} {
		if !strings.Contains(after, want) {
			t.Fatalf("restored settings lost %s", want)
		}
	}
}

func TestReviewPiRestoreAfterAddingUnrelatedExtension(t *testing.T) {
	m, host := testManager(t)
	h := host["pi"]
	old := filepath.Join(h, "extensions", "auto-name")
	writeFixture(t, filepath.Join(old, "index.js"), "import {x} from './core.js'; pi.registerCommand('auto-name', {});")
	settings := filepath.Join(h, "settings.json")
	writeFixture(t, settings, `{"extensions":["extensions/auto-name/index.js","extensions/other.js"]}`)
	mustInvoke(t, m, "enable", request{ID: "pi", Takeover: true})
	mustInvoke(t, m, "disable", request{ID: "pi"})
	writeFixture(t, settings, `{"extensions":["extensions/other.js","extensions/added.js"]}`)
	mustInvoke(t, m, "restore", request{ID: "pi"})
	var got struct{ Extensions []string }
	want := []string{"extensions/auto-name/index.js", "extensions/other.js", "extensions/added.js"}
	if err := json.Unmarshal([]byte(readFixture(t, settings)), &got); err != nil || !reflect.DeepEqual(got.Extensions, want) {
		t.Fatalf("extensions after restore: %v, %v", got.Extensions, err)
	}
	if err := m.UninstallReady(context.Background()); err != nil {
		t.Fatal(err)
	}
}
