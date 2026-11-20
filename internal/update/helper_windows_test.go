//go:build windows

package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestRestoreInstallFilesHandlesLegacyMissingFiles(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "install")
	backupDir := filepath.Join(root, "backup")
	backupInstall := filepath.Join(backupDir, "install")
	if err := os.MkdirAll(backupInstall, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(installDir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(backupInstall, "AgentTitleManager.exe"), "old")
	manifest, err := json.Marshal(installBackupManifest{Files: []string{"AgentTitleManager.exe"}})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(backupDir, installBackupManifestName), string(manifest))
	write(filepath.Join(installDir, "AgentTitleManager.exe"), "new")
	write(filepath.Join(installDir, "atm-update.exe"), "new helper")
	if err := os.MkdirAll(filepath.Join(installDir, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(installDir, "plugins", "new.dll"), "new plugin")

	if err := restoreInstallFiles(installDir, backupDir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(installDir, "AgentTitleManager.exe")); err != nil || string(got) != "old" {
		t.Fatalf("legacy file was not restored: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "atm-update.exe")); !os.IsNotExist(err) {
		t.Fatalf("new helper should be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("new plugins should be removed, got %v", err)
	}
}

func TestRollbackRestoresOwnedWindowsRegistrationValues(t *testing.T) {
	path := `Software\AgentTitleManagerTests\` + filepath.Base(t.TempDir())
	dir := t.TempDir()
	key, existing, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS|registry.WOW64_32KEY)
	if err != nil || existing {
		t.Fatalf("temporary key: %v, existing=%v", err, existing)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	defer key.Close()
	for _, err := range []error{
		key.SetStringValue("InstallLocation", dir),
		key.SetStringValue("DisplayVersion", "0.4.0"),
		key.SetDWordValue("NoModify", 7),
		key.SetStringValue("Unrelated", "keep"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	restore, want, err := snapshotUninstallKey(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		key.SetStringValue("DisplayVersion", "0.4.1"),
		key.SetDWordValue("NoModify", 1),
		key.SetStringValue("DisplayName", "new value"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	got, err := readUninstallValues(key)
	unrelated, _, unrelatedErr := key.GetStringValue("Unrelated")
	if err != nil || !reflect.DeepEqual(got, want) || unrelatedErr != nil || unrelated != "keep" {
		t.Fatalf("registration rollback: %+v %v, unrelated=%q %v", got, err, unrelated, unrelatedErr)
	}
	h := NewWindowsHelper(Handoff{Isolated: true})
	if err := h.verifyRegistryVersion(dir, "0.4.1"); err != nil {
		t.Fatalf("isolated version unexpectedly checked the formal registration: %v", err)
	}
}

func TestHelperDepsUseInjectedPublicKey(t *testing.T) {
	dir := fixtureWithSignedInstaller(t)
	key := publicHexFromFixture(t, dir)
	h := handoffFixture(dir, key)
	previous := updatePublicKey
	updatePublicKey = key
	defer func() { updatePublicKey = previous }()
	if err := NewWindowsHelper(h).Deps(PublicKey()).verify(); err != nil {
		t.Fatalf("helper did not receive the injected public key: %v", err)
	}
}
