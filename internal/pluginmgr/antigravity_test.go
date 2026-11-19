package pluginmgr

import (
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
