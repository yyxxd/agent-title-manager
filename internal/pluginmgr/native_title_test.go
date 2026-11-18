package pluginmgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClaudeVerifiedTitleUsesBundledObservation(t *testing.T) {
	bundle := os.Getenv("ATM_TEST_ADAPTER_BUNDLE")
	if bundle == "" {
		t.Skip("requires the explicitly supplied adapter release")
	}
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := t.TempDir()
	const id = "12345678-1234-1234-1234-123456789abc"
	const frame = `{"type":"custom-title","sessionId":"` + id + `","customTitle":"标题 A"}` + "\n"
	project := filepath.Join(host, "projects", "scratch")
	plugin := filepath.Join(bundle, "claude")
	writeFixture(t, filepath.Join(project, id+".jsonl"), frame)
	if title, err := adapterTitle(ctx, node, "claude", id, plugin, host); err != nil || title != "标题 A" {
		t.Fatalf("verified title %q, error %v", title, err)
	}
	sidecar := filepath.Join(project, id, "custom-title.json")
	writeFixture(t, sidecar, `{"customTitle":"手动标题 B"}`)
	if _, err := adapterTitle(ctx, node, "claude", id, plugin, host); err == nil {
		t.Fatal("conflicting native title sources were accepted")
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(host, "projects", "duplicate", id+".jsonl"), frame)
	if _, err := adapterTitle(ctx, node, "claude", id, plugin, host); err == nil {
		t.Fatal("ambiguous native session files were accepted")
	}
}
