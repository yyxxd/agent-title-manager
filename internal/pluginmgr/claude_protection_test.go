package pluginmgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClaudeProtectionReaderUsesExplicitAccount(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		node, err = exec.LookPath("node")
	}
	if err != nil {
		t.Fatal(err)
	}
	a, b := t.TempDir(), t.TempDir()
	plugin, err := filepath.Abs("../../plugins/claude")
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("projects", "isolated", nativeFixture+".jsonl")
	writeFixture(t, filepath.Join(a, rel), `{"type":"custom-title","sessionId":"`+nativeFixture+`","customTitle":"A decoy"}`+"\n")
	writeFixture(t, filepath.Join(b, rel), `{"type":"custom-title","sessionId":"`+nativeFixture+`","customTitle":"Before lock"}`+"\n"+`{"type":"custom-title","sessionId":"`+nativeFixture+`","customTitle":"Last manual title"}`+"\n")
	read := func(root string) (string, error) {
		return adapterTitle(t.Context(), node, "claude", nativeFixture, plugin, a, root)
	}
	if got, err := read(""); err != nil || got != "A decoy" {
		t.Fatalf("default A: %q %v", got, err)
	}
	if got, err := read(b); err != nil || got != "Last manual title" {
		t.Fatalf("scoped B: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(b, rel)); err != nil {
		t.Fatal(err)
	}
	if got, err := read(b); err == nil || got != "" {
		t.Fatalf("missing B fell back to A: %q %v", got, err)
	}
}
