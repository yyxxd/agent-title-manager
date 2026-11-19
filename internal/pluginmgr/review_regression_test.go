package pluginmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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

func TestCodexDisablePreservesExistingHooksAndUserAddedHandler(t *testing.T) {
	m, host := testManager(t)
	h := host["codex"]
	file := filepath.Join(h, "hooks.json")
	hookFixture(t, file, map[string]string{
		"type": "command", "command": "echo non-windows-hook", "commandWindows": "echo existing-windows-hook",
	})
	mustInvoke(t, m, "enable", request{ID: "codex"})
	enabled := readFixture(t, file)
	writeFixture(t, file, strings.Replace(enabled, `"hooks": [`, `"hooks": [{"type":"command","command":"echo user-added"},`, 1))
	mustInvoke(t, m, "disable", request{ID: "codex"})
	after := readFixture(t, file)
	for _, want := range []string{"echo non-windows-hook", "echo existing-windows-hook", "echo user-added"} {
		if !strings.Contains(after, want) {
			t.Fatalf("disabled settings lost %s", want)
		}
	}
	if strings.Contains(after, "test-entry.mjs") {
		t.Fatal("disabled settings retained manager-owned hook")
	}
}

func TestPiDisableAfterAddingUnrelatedExtension(t *testing.T) {
	m, host := testManager(t)
	h := host["pi"]
	settings := filepath.Join(h, "settings.json")
	writeFixture(t, settings, `{"extensions":["extensions/other.js"]}`)
	mustInvoke(t, m, "enable", request{ID: "pi"})
	enabled := readFixture(t, settings)
	if !strings.Contains(enabled, "test-entry.mjs") {
		t.Fatal("enable did not add Pi extension")
	}
	writeFixture(t, settings, strings.Replace(enabled, `"extensions/other.js"`, `"extensions/other.js","extensions/added.js"`, 1))
	mustInvoke(t, m, "disable", request{ID: "pi"})
	var got struct{ Extensions []string }
	want := []string{"extensions/other.js", "extensions/added.js"}
	if err := json.Unmarshal([]byte(readFixture(t, settings)), &got); err != nil || !reflect.DeepEqual(got.Extensions, want) {
		t.Fatalf("extensions after disable: %v, %v", got.Extensions, err)
	}
	if err := m.UninstallReady(context.Background()); err != nil {
		t.Fatal(err)
	}
}
