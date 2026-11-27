package pluginmgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneProtectionReadsNativeTitleWithoutHandle(t *testing.T) {
	const title = "Protected native title"
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", filepath.Join(root, "agy-config"))
	data := filepath.Join(root, "agy-data")
	t.Setenv("ANTIGRAVITY_DATA_DIR", data)
	m := New(filepath.Join(root, "manager"), filepath.Join(root, "application", "plugins"))
	pi := release{Manifest: Manifest{ID: "pi"}, Dir: "pi"}
	ag := release{Manifest: Manifest{ID: "antigravity"}, Dir: "agy"}
	for _, rel := range []release{pi, ag} {
		if err := os.MkdirAll(m.packageDir(rel), 0700); err != nil {
			t.Fatal(err)
		}
	}
	source, err := filepath.Abs("../../plugins/antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if err := copyLocal(t.Context(), source, m.packageDir(ag)); err != nil {
		t.Fatal(err)
	}
	if err := m.save(registry{Items: map[string]record{"pi": {release: pi, Enabled: true}, "antigravity": {release: ag, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	piPath := filepath.Join(root, "pi", "sessions", "--isolated--", "2026-11-26T10-00-00_"+nativeFixture+".jsonl")
	writeFixture(t, piPath, `{"type":"session","version":3,"id":"`+nativeFixture+`"}`+"\n"+`{"type":"session_info","name":"`+title+`"}`+"\n")
	writeFixture(t, filepath.Join(data, "brain", nativeFixture, ".system_generated", "logs", "transcript.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(data, "annotations", nativeFixture+".pbtxt"), `title: "`+title+`"`+"\n")
	for _, agent := range []string{"pi", "antigravity"} {
		t.Run(agent, func(t *testing.T) {
			got, err := m.ReadNativeTitle(t.Context(), agent, nativeFixture, "")
			if err != nil || got != title {
				t.Fatalf("standalone %s read: title=%q err=%v; want %q", agent, got, err, title)
			}
		})
	}
	t.Run("duplicate Pi transcript", func(t *testing.T) {
		dup := filepath.Join(root, "pi", "sessions", "--second--", filepath.Base(piPath))
		writeFixture(t, dup, readFixture(t, piPath))
		if got, err := m.ReadNativeTitle(t.Context(), "pi", nativeFixture, ""); got != "" || !errors.Is(err, ErrNativePending) {
			t.Fatalf("ambiguous Pi transcript read as %q, %v", got, err)
		}
	})
	t.Run("Antigravity conflicting roots", func(t *testing.T) {
		duplicate := filepath.Join(root, ".gemini", "antigravity", "brain", nativeFixture, ".system_generated", "logs", "transcript.jsonl")
		writeFixture(t, duplicate, "{}\n")
		if got, err := m.ReadNativeTitle(t.Context(), "antigravity", nativeFixture, ""); got != "" || !errors.Is(err, ErrNativePending) {
			t.Fatalf("ambiguous Antigravity transcript read as %q, %v", got, err)
		}
	})

}
