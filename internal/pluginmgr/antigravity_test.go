package pluginmgr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravityHooksPreserveOtherGroups(t *testing.T) {
	m, hosts := testManager(t)
	hooks := `{"orca-status":{"Stop":[{"type":"command","command":"status"}]}}`
	if err := os.MkdirAll(hosts["antigravity"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hosts["antigravity"], "hooks.json"), []byte(hooks), 0600); err != nil {
		t.Fatal(err)
	}
	mustInvoke(t, m, "enable", request{ID: "antigravity"})
	state, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	got := string(state.Items["antigravity"].Registration[0].File)
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		t.Fatal("invalid hooks")
	}
	if _, ok := root["orca-status"]; !ok || !strings.Contains(string(root["agent-title-manager"]), "SessionStart") {
		t.Fatalf("hooks not preserved: %s", data)
	}
	mustInvoke(t, m, "disable", request{ID: "antigravity"})
	data, _ = os.ReadFile(got)
	root = map[string]json.RawMessage{}
	_ = json.Unmarshal(data, &root)
	if _, ok := root["orca-status"]; !ok {
		t.Fatal("other hook removed")
	}
	if _, ok := root["agent-title-manager"]; ok {
		t.Fatal("managed group remains")
	}
}

func TestAntigravityBundledUpgradeRemovesLegacyHookDuplicates(t *testing.T) {
	m, hosts := testManager(t)
	const id = "antigravity"
	dir := filepath.Join(m.bundled, id)
	setVersion := func(version string) {
		manifest := Manifest{1, id, "Antigravity", version, "agent", id, "index.mjs"}
		wire, _ := json.Marshal(manifest)
		writeFixture(t, filepath.Join(dir, manifestName), string(wire))
		writeFixture(t, filepath.Join(dir, manifest.Entry), "")
	}
	setVersion("0.1.0")
	mustInvoke(t, m, "enable", request{ID: id})
	state, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(hosts[id], "hooks.json")
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, settings)), &root); err != nil {
		t.Fatal(err)
	}
	group := root["agent-title-manager"]
	for _, event := range events(id) {
		node := filepath.Join(m.bundled, "node.exe")
		path := filepath.Join(m.packageDir(state.Items[id].release), "index.mjs")
		legacy := map[string]any{"type": "command", "command": `"` + node + `" "` + path + `" ` + event.arg}
		encode := func(value string) string {
			return `([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(value)) + `')))`
		}
		encoded := map[string]any{"type": "command", "command": `C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe` + antigravityLauncher + encode(node) + " " + encode(path) + `" ` + event.arg}
		group[event.name] = append(group[event.name], legacy, legacy, encoded, encoded)
	}
	root["other-group"] = map[string][]map[string]any{"Stop": {{"type": "command", "command": "custom"}}}
	group["Stop"] = append(group["Stop"], map[string]any{"type": "command", "command": "user-hook"})
	wire, _ := json.Marshal(root)
	writeFixture(t, settings, string(wire))
	setVersion("0.2.0")
	result, err := m.AutoUpdateBuiltins(context.Background())
	if err != nil || len(result.Upgraded) != 1 || len(result.Failed) != 0 || result.Upgraded[0].ToVersion != "0.2.0" {
		t.Fatalf("upgrade = %+v, %v", result, err)
	}
	var upgraded map[string]map[string][]map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, settings)), &upgraded); err != nil {
		t.Fatal(err)
	}
	state, err = m.load()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events(id) {
		count := 0
		for _, hook := range upgraded["agent-title-manager"][event.name] {
			cmd := hook["command"].(string)
			switch cmd {
			case "user-hook":
			case state.Items[id].Entry + " " + event.arg:
				count++
			default:
				t.Fatalf("unexpected hook after upgrade: %q", cmd)
			}
		}
		if count != 1 {
			t.Fatalf("%s: got %d installed hooks", event.name, count)
		}
	}
	if upgraded["other-group"]["Stop"][0]["command"] != "custom" || len(upgraded["agent-title-manager"]["Stop"]) != 2 {
		t.Fatal("user hooks not preserved")
	}
}
