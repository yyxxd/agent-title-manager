package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func TestRepairNotes(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPDATE_PUBLIC_KEY", hex.EncodeToString(public))
	t.Setenv("ATM_SIGNING_KEY", hex.EncodeToString(private.Seed()))
	const version = "0.4.1"
	const filename = "AgentTitleManager-0.4.1-windows-x64-setup.exe"
	const notes = "# 中文更新主题\n## 修复\n- **保留说明**：包含 `#` 与中文。"
	installerData := []byte("fixture installer contents")
	digest := sha256.Sum256(installerData)
	original := update.Manifest{
		Version: version, Arch: "windows-x64", File: filename, Size: int64(len(installerData)),
		SHA256: hex.EncodeToString(digest[:]), Notes: "English commit message",
	}
	for _, tc := range []struct {
		name          string
		change        func(*update.Manifest)
		wrongKey      bool
		wrongName     bool
		wrongBytes    bool
		wantErrorText string
	}{
		{name: "only notes and signature change"},
		{name: "bad signature", change: func(m *update.Manifest) { m.Signature = "00" }, wantErrorText: "原清单验签失败"},
		{name: "wrong trusted public key", wrongKey: true, wantErrorText: "可信公钥不匹配"},
		{name: "wrong version", change: func(m *update.Manifest) { m.Version = "0.4.2" }, wantErrorText: "待修复版本不一致"},
		{name: "wrong architecture", change: func(m *update.Manifest) { m.Arch = "windows-arm64" }, wantErrorText: "原清单验签失败"},
		{name: "wrong manifest filename", change: func(m *update.Manifest) { m.File = "other.exe" }, wantErrorText: "安装包文件名"},
		{name: "wrong installer filename", wrongName: true, wantErrorText: "安装包文件名"},
		{name: "wrong size", change: func(m *update.Manifest) { m.Size++ }, wantErrorText: "实际大小或 SHA-256"},
		{name: "wrong installer hash", wrongBytes: true, wantErrorText: "实际大小或 SHA-256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			existing := filepath.Join(dir, "original.json")
			installer := filepath.Join(dir, filename)
			notesFile := filepath.Join(dir, "notes.md")
			output := filepath.Join(dir, "update-manifest.json")
			source := original
			if tc.change != nil {
				tc.change(&source)
			}
			if source.Signature == "" {
				source.Signature, err = update.Sign(private, source)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.wrongKey {
				t.Setenv("UPDATE_PUBLIC_KEY", hex.EncodeToString(make([]byte, ed25519.PublicKeySize)))
			}
			if tc.wrongName {
				installer = filepath.Join(dir, "other.exe")
			}
			data := bytes.Clone(installerData)
			if tc.wrongBytes {
				data[0] ^= 1
			}
			raw, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string][]byte{existing: raw, installer: data, notesFile: []byte(notes)} {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = runRepair([]string{"-version", version, "-existing", existing, "-installer", installer, "-notes-file", notesFile, "-output", output})
			if tc.wantErrorText != "" {
				if _, statErr := os.Stat(output); err == nil || !strings.Contains(err.Error(), tc.wantErrorText) || !os.IsNotExist(statErr) {
					t.Fatalf("expected %q without an output, got repair error %v and stat error %v", tc.wantErrorText, err, statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertRepaired(t, public, output, source, notes)
			for path, originalData := range map[string][]byte{existing: raw, installer: data} {
				current, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(current, originalData) {
					t.Fatalf("source file changed: %s (%v)", path, err)
				}
			}
		})
	}
}

func assertRepaired(t *testing.T, public ed25519.PublicKey, path string, source update.Manifest, notes string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var repaired update.Manifest
	if err := json.Unmarshal(raw, &repaired); err != nil {
		t.Fatal(err)
	}
	if err := update.Verify(public, repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.Signature == source.Signature {
		t.Fatal("signature did not change")
	}
	source.Notes, source.Signature = notes, repaired.Signature
	if repaired != source {
		t.Fatalf("expected only new notes and signature, got %+v", repaired)
	}
}
