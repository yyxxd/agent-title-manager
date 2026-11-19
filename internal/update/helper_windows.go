package update

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	_ "modernc.org/sqlite"
)

// WindowsHelper performs the real handoff operations on Windows.
type WindowsHelper struct {
	Timeout time.Duration
	handoff Handoff
}

func NewWindowsHelper(h Handoff) *WindowsHelper {
	return &WindowsHelper{Timeout: 5 * time.Minute, handoff: h}
}

// Deps returns the platform operations for the handoff sequence. The public
// key is compiled into the helper and used to re-verify the handoff.
func (h *WindowsHelper) Deps(publicKeyHex string) helperDeps {
	return helperDeps{
		verify:       func() error { return verifyHandoff(h.handoff, publicKeyHex) },
		waitExit:     h.waitExit,
		prepare:      prepareSnapshot,
		install:      installNSIS,
		checkVersion: checkInstalledVersion,
		verifyData:   verifyDatabase,
		verifyVersion: verifyRegistryVersion,
		restart:      restartApp,
	}
}

// waitExit polls the process handle without terminating it, so in-flight work
// finishes through the app's own shutdown path.
func (h *WindowsHelper) waitExit(pid uint32) error {
	if pid == 0 {
		return errors.New("缺少应用进程号")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		// Only "no such process" means the app already exited; access denied
		// or any other failure must be reported instead of assumed.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("无法确认应用进程状态: %w", err)
	}
	defer windows.CloseHandle(handle)
	deadline := time.Now().Add(h.Timeout)
	for time.Now().Before(deadline) {
		result, err := windows.WaitForSingleObject(handle, 500)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
	}
	return errors.New("等待应用退出超时")
}

// installNSIS launches the installer directly with an argument vector; no
// shell is involved and no user text is concatenated into a command string.
func installNSIS(installer, installDir string, isolated bool) error {
	// NSIS /D= requires backslashes; filepath.Clean normalizes on Windows.
	// Always pass the target so custom (non-default) install paths work.
	installDir = filepath.Clean(installDir)
	args := []string{"/S"}
	if isolated {
		args = append(args, "/NoIntegration")
	}
	args = append(args, "/D="+installDir)
	cmd := exec.Command(installer, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装程序失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func checkInstalledVersion(installDir, want string) error {
	exe := filepath.Join(installDir, "AgentTitleManager.exe")
	output, err := exec.Command(exe, "--version").Output()
	if err != nil {
		return fmt.Errorf("无法读取已安装版本: %w", err)
	}
	if got := strings.TrimSpace(string(output)); got != want {
		return fmt.Errorf("版本不符: 期望 %s，实际 %s", want, got)
	}
	return nil
}

// verifyRegistryVersion checks the uninstall record for a normal (non-isolated)
// install, including the custom target path. Isolated installs skip integration.
func verifyRegistryVersion(installDir, want string) error {
	if os.Getenv("ATM_UPDATE_ISOLATED") == "1" {
		return nil
	}
	const keyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\AgentTitleManager`
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("无法读取安装登记信息: %w", err)
	}
	defer key.Close()
	version, _, err := key.GetStringValue("DisplayVersion")
	if err != nil || version != want {
		return fmt.Errorf("登记版本不符: 期望 %s，实际 %s (%v)", want, version, err)
	}
	location, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !strings.EqualFold(filepath.Clean(location), filepath.Clean(installDir)) {
		return fmt.Errorf("登记路径不符: 期望 %s，实际 %s (%v)", installDir, location, err)
	}
	return nil
}

func restartApp(path string) error {
	cmd := exec.Command(path, "--background")
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	return cmd.Start()
}

// Known install files restored on rollback. We never clear the whole install
// directory: user-added files and custom install paths must survive a failure.
var installFiles = []string{
	"AgentTitleManager.exe", "atm-bridge.exe", "atm-update.exe",
	"LICENSE.txt", "OIL-CODEX-TITLE-LICENSE.txt", "Uninstall.exe",
}

// prepareSnapshot makes a consistent SQLite snapshot and copies only the
// known install files. A failed or corrupt DB snapshot aborts the update and
// leaves the backup directory in place for diagnosis.
func prepareSnapshot(installDir, dataDir string) (func() error, error) {
	backupDir := filepath.Join(dataDir, "update-backup")
	if err := os.RemoveAll(backupDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(backupDir, "install"), 0700); err != nil {
		return nil, err
	}
	if _, err := vacuumDatabase(filepath.Join(dataDir, "engine.sqlite3"), filepath.Join(backupDir, "engine.sqlite3")); err != nil {
		return nil, fmt.Errorf("数据库一致备份失败（备份保留于 %s）: %w", backupDir, err)
	}
	for _, name := range installFiles {
		source := filepath.Join(installDir, name)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := copyFile(source, filepath.Join(backupDir, "install", name)); err != nil {
			return nil, err
		}
	}
	plugins := filepath.Join(installDir, "plugins")
	if _, err := os.Stat(plugins); err == nil {
		if err := copyTree(plugins, filepath.Join(backupDir, "install", "plugins")); err != nil {
			return nil, err
		}
	}
	return func() error { return restoreInstallFiles(installDir, backupDir) }, nil
}

// restoreInstallFiles overwrites only the files we backed up and verifies
// each one exists afterwards.
func restoreInstallFiles(installDir, backupDir string) error {
	source := filepath.Join(backupDir, "install")
	for _, name := range installFiles {
		from := filepath.Join(source, name)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := copyFile(from, filepath.Join(installDir, name)); err != nil {
			return err
		}
	}
	plugins := filepath.Join(source, "plugins")
	if _, err := os.Stat(plugins); err == nil {
		if err := copyTree(plugins, filepath.Join(installDir, "plugins")); err != nil {
			return err
		}
	}
	for _, name := range installFiles {
		if _, err := os.Stat(filepath.Join(installDir, name)); err != nil {
			return fmt.Errorf("恢复后缺少 %s: %w", name, err)
		}
	}
	return nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// vacuumDatabase uses SQLite itself for a transactionally consistent copy
// and verifies the result before accepting it.
func vacuumDatabase(source, target string) (bool, error) {
	exists, err := fileExists(source)
	if err != nil || !exists {
		return false, err
	}
	db, err := sql.Open("sqlite", "file:"+source+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer db.Close()
	quoted := strings.ReplaceAll(target, "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
		return false, err
	}
	check, err := sql.Open("sqlite", "file:"+target+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer check.Close()
	var result string
	if err := check.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return false, fmt.Errorf("备份数据库校验失败: %v %s", err, result)
	}
	return true, nil
}

// verifyDatabase confirms the live database is still intact and holds the
// same user data as the snapshot after the installer ran.
func verifyDatabase(dataDir string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "engine.sqlite3")+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return fmt.Errorf("用户数据校验失败: %v %s", err, result)
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		return err
	}
	backupDB, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "update-backup", "engine.sqlite3")+"?mode=ro")
	if err != nil {
		return err
	}
	defer backupDB.Close()
	var saved int
	if err := backupDB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&saved); err != nil {
		return err
	}
	if saved != sessions {
		return fmt.Errorf("用户数据未保留: 备份 %d 条，现有 %d 条", saved, sessions)
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		return errors.Join(err, out.Close())
	})
}
