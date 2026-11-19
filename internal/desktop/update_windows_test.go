package desktop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func TestUpdateDisabledWithoutKey(t *testing.T) {
	dir := t.TempDir()
	c, err := NewUpdateController(dir, dir, "0.1.6", "", "")
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.snapshot()
	if err != nil || view.Enabled || view.Current != "0.1.6" {
		t.Fatalf("unexpected disabled view: %+v %v", view, err)
	}
	if _, err := c.install(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, update.ErrNoTrustRoot) {
		t.Fatalf("expected disabled install error, got %v", err)
	}
}

func TestUpdateCheckAndInstallFlow(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	installer := []byte("signed-installer")
	sum := sha256.Sum256(installer)
	manifest := update.Manifest{Version: "0.1.7", Arch: "windows-x64", File: "setup.exe",
		Size: int64(len(installer)), SHA256: hex.EncodeToString(sum[:]), Notes: "说明"}
	sig, _ := update.Sign(private, manifest)
	manifest.Signature = sig
	release := map[string]any{"tag_name": "v0.1.7", "draft": false, "prerelease": false,
		"body": "测试说明", "assets": []map[string]any{
		{"name": "update-manifest.json", "browser_download_url": "/manifest"},
		{"name": "setup.exe", "size": len(installer), "browser_download_url": "/installer"},
	}}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			assets := release["assets"].([]map[string]any)
			for _, asset := range assets {
				asset["browser_download_url"] = server.URL + asset["browser_download_url"].(string)
			}
			_ = json.NewEncoder(w).Encode(release)
		case "/manifest":
			_ = json.NewEncoder(w).Encode(manifest)
		case "/installer":
			_, _ = w.Write(installer)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	dataDir := t.TempDir()
	c, err := NewUpdateController(dir, dataDir, "0.1.6", hex.EncodeToString(public), server.URL+"/release")
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.refresh(context.Background())
	if err != nil || view.Available == nil || view.Available.Version != "0.1.7" {
		t.Fatalf("check failed: %+v %v", view, err)
	}
	// Provide a helper file to copy; launch is stubbed so no real exe runs.
	if err := os.WriteFile(filepath.Join(dir, "atm-update.exe"), []byte("stub"), 0600); err != nil {
		t.Fatal(err)
	}
	launched := 0
	var launchedBinary, launchedParam string
	c.launch = func(binary, param string) error {
		launched++
		launchedBinary, launchedParam = binary, param
		return nil
	}
	installDir := filepath.Join(t.TempDir(), "app")
	result, err := c.install(context.Background(), json.RawMessage(`{"installDir":`+mustJSON(installDir)+`}`))
	if err != nil || result["started"] != true {
		t.Fatalf("install failed: %+v %v", result, err)
	}
	if want := filepath.Join(dataDir, "updates", "atm-update.exe"); launchedBinary != want {
		t.Fatalf("helper copied from wrong root: got %q want %q", launchedBinary, want)
	}
	if want := filepath.Join(dataDir, "update-handoff.json"); launchedParam != want {
		t.Fatalf("handoff written to wrong root: got %q want %q", launchedParam, want)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "update-handoff.json")); err != nil {
		t.Fatal("handoff not written:", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "updates", "setup.exe")); err != nil {
		t.Fatal("verified installer not saved:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "updates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("update files leaked into app directory:", err)
	}
	if launched != 1 {
		t.Fatalf("helper launched %d times", launched)
	}
}

func TestUpdateInstallRejectsUnknownParams(t *testing.T) {
	c, err := NewUpdateController(t.TempDir(), t.TempDir(), "0.1.6", strings.Repeat("0", 64), "")
	if err != nil {
		t.Fatal(err)
	}
	c.enabled = true
	_, err = c.install(context.Background(), json.RawMessage(`{"evil":1}`))
	if err == nil {
		t.Fatal("expected invalid params error")
	}
}

func mustJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
