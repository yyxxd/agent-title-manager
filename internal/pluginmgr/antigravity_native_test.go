package pluginmgr

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

func TestAntigravityACPPreflightReadsVerifiedNativeTranscriptRoot(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	home := t.TempDir()
	orca := filepath.Join(home, "isolated Orca")
	t.Setenv("ORCA_USER_DATA_PATH", orca)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("ANTIGRAVITY_DATA_DIR", "")
	const id = "b68b5391-bfed-45ae-948e-259bb4abf012"
	parent := filepath.Join(orca, "antigravity-chat", "antigravity-acp")
	plugin, err := filepath.Abs("../../plugins/antigravity")
	if err != nil {
		t.Fatal(err)
	}
	read := func() (string, error) {
		return adapterTitle(context.Background(), node, "antigravity", id, plugin, home)
	}
	annotation := filepath.Join(parent, "annotations", id+".pbtxt")
	writeFixture(t, annotation, `title:"替换标题" permission_preset:3`)
	if _, err := read(); err != orcasync.ErrNativePending {
		t.Fatalf("unverified annotation root: %v", err)
	}
	writeFixture(t, filepath.Join(parent, "brain", id, ".system_generated", "logs", "transcript.jsonl"), "{}")
	if got, err := read(); err != nil || got != "替换标题" {
		t.Fatalf("ACP preflight recovered %q, %v", got, err)
	}
	writeFixture(t, annotation, `title:"broken`)
	if _, err := read(); err != orcasync.ErrNativePending {
		t.Fatalf("corrupt annotation read: %v", err)
	}
}
