package pluginmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgrade031PreservesSwitchesAndRetiresHelperFiles(t *testing.T) {
	m, _ := testManager(t)
	m.SetBuildVersion("0.4.0")
	// v0.3.1 在同一张 items 表中存 Orca 标志，没有集成清单。
	writeFixture(t, m.stateFile(), `{"items":{"orca":{"manifest":{"schemaVersion":0,"id":"","name":"","version":"","kind":"","host":"","entry":""},"dir":"","enabled":true}}}`)
	if on, err := m.IntegrationEnabled(t.Context(), OrcaIntegration); err != nil || !on {
		t.Fatalf("legacy switch: %v %v", on, err)
	}
	oldPaths := make(map[string]string)
	for _, id := range builtinOrder {
		version := map[string]string{"codex": "0.1.1", "claude": "0.1.4", "pi": "0.1.1", "antigravity": "0.2.0"}[id]
		bundle := filepath.Join(m.bundled, id)
		packageFixture(t, bundle, id, id, version)
		writeFixture(t, filepath.Join(bundle, "orca.mjs"), "legacy")
		if id == "antigravity" {
			writeFixture(t, filepath.Join(bundle, "orca-binding.mjs"), "legacy")
		}
		mustInvoke(t, m, "enable", request{ID: id})
		if id == "pi" {
			mustInvoke(t, m, "disable", request{ID: id})
		}
		state, err := m.load()
		if err != nil {
			t.Fatal(err)
		}
		oldPaths[id] = m.packageDir(state.Items[id].release)
		for _, name := range []string{"orca.mjs", "orca-binding.mjs"} {
			if err := os.Remove(filepath.Join(bundle, name)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		packageFixture(t, bundle, id, id, nativeOnlyVersion[id])
	}
	got, err := m.AutoUpdateBuiltins(t.Context())
	if err != nil || len(got.Failed) != 0 || len(got.Upgraded) != 4 {
		t.Fatalf("upgrade: %+v %v", got, err)
	}
	state, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range builtinOrder {
		item := m.item(state, id)
		if item.Kind != "builtin" || item.Version != nativeOnlyVersion[id] || item.Enabled != (id != "pi") {
			t.Fatalf("%s migrated to %+v", id, item)
		}
		for _, dir := range []string{oldPaths[id], m.packageDir(state.Items[id].release)} {
			for _, name := range []string{"orca.mjs", "orca-binding.mjs"} {
				if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("legacy helper remains at %s: %v", dir, err)
				}
			}
		}
	}
	if item := m.item(state, OrcaIntegration); !item.Enabled || item.Kind != "integration" || item.Version != "0.4.0" {
		t.Fatalf("Orca migrated to %+v", item)
	}
}
