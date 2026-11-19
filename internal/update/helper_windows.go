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
	_ "modernc.org/sqlite"
)

// WindowsHelper performs the real handoff operations on Windows.
type WindowsHelper struct {
	Timeout time.Duration
}

func NewWindowsHelper() *WindowsHelper { return &WindowsHelper{Timeout: 5 * time.Minute} }

// Deps returns the platform operations for the handoff sequence.
func (h *WindowsHelper) Deps() helperDeps { return h.deps() }

func (h *WindowsHelper) deps() helperDeps {
	return helperDeps{
		waitExit:     h.waitExit,
		prepare:      prepareSnapshot,
		install:      installNSIS,
		checkVersion: checkInstalledVersion,
		verifyData:   verifyDatabase,
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
		// Already exited is a valid state.
		return nil
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
	installDir = filepath.Clean(installDir)
	args := []string{"/S"}
	if isolated {
		args = append(args, "/NoIntegration", "/D="+installDir)
	}
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

func restartApp(path string) error {
	cmd := exec.Command(path, "--background")
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	return cmd.Start()
}

// prepareSnapshot makes a consistent SQLite snapshot via VACUUM INTO and a
// file copy of the live install directory; restore reverses the install.
func prepareSnapshot(installDir, dataDir string) (func() error, error) {
	backupDir := filepath.Join(dataDir, "update-backup")
	if err := os.RemoveAll(backupDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return nil, err
	}
	if _, err := vacuumDatabase(filepath.Join(dataDir, "engine.sqlite3"), filepath.Join(backupDir, "engine.sqlite3")); err != nil {
		return nil, err
	}
	installBackup := filepath.Join(backupDir, "install")
	if err := copyTree(installDir, installBackup); err != nil {
		return nil, err
	}
	return func() error {
		if err := os.RemoveAll(installDir); err != nil {
			return err
		}
		return copyTree(installBackup, installDir)
	}, nil
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
