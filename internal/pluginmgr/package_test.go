package pluginmgr

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackGitHubRejectsUnsafeEntries(t *testing.T) {
	for _, c := range []struct {
		name string
		mode os.FileMode
	}{
		{"repo-pin/../escape", 0600},
		{"repo-pin/C:/escape", 0600},
		{"repo-pin/folder\\escape", 0600},
		{"repo-pin/CON.txt", 0600},
		{"repo-pin/entry.", 0600},
		{"repo-pin/atm-runtime.json", 0600},
		{"wrong-pin/entry.mjs", 0600},
		{"repo-pin/link.mjs", os.ModeSymlink | 0777},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := zipTestFile(t, c.name, c.mode, "anything")
			if err := unpackGitHub(context.Background(), data, "repo-pin", t.TempDir()); err == nil {
				t.Fatal("unexpectedly accepted unsafe archive")
			}
		})
	}
}

func TestInstallRejectsReservedIDAndInvalidManifest(t *testing.T) {
	m, _ := testManager(t)
	for _, c := range []struct{ id, host, version string }{
		{"codex", "external", "1.0.0"},
		{"orca", "external", "1.0.0"},
		{"untrusted-agent", "pi", "1.0.0"},
		{"malformed-version", "external", "1.0.0-02"},
	} {
		dir := t.TempDir()
		packageFixture(t, dir, c.id, c.host, c.version)
		if _, err := invoke(t, m, "install", request{Source: "local", Path: dir}); err == nil {
			t.Fatalf("unexpected successful install for %s", c.id)
		}
	}
	state, err := m.load()
	if err != nil || len(state.Items) != 0 {
		t.Fatalf("invalid package left state: %+v, %v", state, err)
	}
	entries, err := os.ReadDir(filepath.Join(m.root, "packages"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid package left staging files: %v, %v", entries, err)
	}
}

func TestUnpackGitHubValidNestedEntry(t *testing.T) {
	data := zipTestFile(t, "repo-pin/sub/entry.mjs", 0600, "hello")
	dir := t.TempDir()
	if err := unpackGitHub(context.Background(), data, "repo-pin", dir); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, filepath.Join(dir, "sub", "entry.mjs")); got != "hello" {
		t.Fatal(got)
	}
}

func TestLivePinnedGitHubDownload(t *testing.T) {
	repo, ref := os.Getenv("ATM_TEST_GITHUB_REPO"), os.Getenv("ATM_TEST_GITHUB_REF")
	if repo == "" || ref == "" {
		t.Skip("requires an explicitly selected public GitHub commit")
	}
	dir := t.TempDir()
	if err := fetchGitHub(context.Background(), repo, ref, dir); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		t.Fatalf("no files extracted, %v", err)
	}
}

func zipTestFile(t *testing.T, path string, mode os.FileMode, content string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	header := &zip.FileHeader{Name: path}
	header.SetMode(mode)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
