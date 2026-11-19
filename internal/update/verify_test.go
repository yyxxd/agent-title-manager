package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyHandoffAcceptsValid(t *testing.T) {
	dir := fixtureWithSignedInstaller(t)
	h := handoffFixture(dir, publicHexFromFixture(t, dir))
	if err := verifyHandoff(h, publicHexFromFixture(t, dir)); err != nil {
		t.Fatalf("valid handoff rejected: %v", err)
	}
}

func TestVerifyHandoffRejectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *Handoff, string)
		want   error
	}{
		{"installer replaced", func(t *testing.T, h *Handoff, _ string) {
			if err := os.WriteFile(h.Installer, []byte("evil"), 0600); err != nil { t.Fatal(err) }
		}, ErrHashMismatch},
		{"manifest signature broken", func(t *testing.T, h *Handoff, _ string) {
			data, _ := os.ReadFile(h.Manifest)
			os.WriteFile(h.Manifest, []byte(strings.Replace(string(data), `"signature":"`, `"signature":"x`, 1)), 0600)
		}, ErrUntrusted},
		{"version mismatch", func(t *testing.T, h *Handoff, _ string) { h.Version = "9.9.9" }, ErrUntrusted},
		{"wrong restart target", func(t *testing.T, h *Handoff, _ string) { h.Restart = filepath.Join(h.DataDir, "evil.exe") }, nil},
		{"relative target", func(t *testing.T, h *Handoff, _ string) { h.InstallDir = "relative" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixtureWithSignedInstaller(t)
			h := handoffFixture(dir, publicHexFromFixture(t, dir))
			tc.mutate(t, &h, dir)
			err := verifyHandoff(h, publicHexFromFixture(t, dir))
			if err == nil {
				t.Fatal("expected verification failure")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyHandoffRejectsWrongPublicKey(t *testing.T) {
	dir := fixtureWithSignedInstaller(t)
	h := handoffFixture(dir, publicHexFromFixture(t, dir))
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := verifyHandoff(h, hex.EncodeToString(other)); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("expected untrusted, got %v", err)
	}
}

func fixtureWithSignedInstaller(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	installDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(installDir, 0700); err != nil { t.Fatal(err) }
	installer := []byte("real-installer-bytes")
	installerPath := filepath.Join(dir, "setup.exe")
	if err := os.WriteFile(installerPath, installer, 0600); err != nil { t.Fatal(err) }
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { t.Fatal(err) }
	sum := sha256.Sum256(installer)
	manifest := Manifest{Version: "0.1.7", Arch: archWindows, File: "setup.exe",
		Size: int64(len(installer)), SHA256: hex.EncodeToString(sum[:]), Notes: "说明"}
	sig, _ := Sign(private, manifest)
	manifest.Signature = sig
	data, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(dir, "update-manifest.json")
	if err := os.WriteFile(manifestPath, data, 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte(hex.EncodeToString(public)), 0600); err != nil { t.Fatal(err) }
	return dir
}

func publicHexFromFixture(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "public.txt"))
	if err != nil { t.Fatal(err) }
	return strings.TrimSpace(string(data))
}

func handoffFixture(dir, public string) Handoff {
	return Handoff{AppPID: 1, Installer: filepath.Join(dir, "setup.exe"),
		InstallDir: filepath.Join(dir, "app"), DataDir: dir, Version: "0.1.7",
		PreviousVersion: "0.1.6", Restart: filepath.Join(dir, "app", "AgentTitleManager.exe"),
		Manifest: filepath.Join(dir, "update-manifest.json"), Isolated: true}
}
