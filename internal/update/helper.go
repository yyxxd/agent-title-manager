package update

import (
	"errors"
	"fmt"
)

// helperDeps holds the platform operations so the handoff sequence can be
// tested without launching real installers or processes.
type helperDeps struct {
	waitExit     func(uint32) error
	prepare      func(installDir, dataDir string) (restore func() error, err error)
	install      func(installer, installDir string, isolated bool) error
	checkVersion func(installDir, version string) error
	verifyData   func(dataDir string) error
	restart      func(path string) error
}

// runHandoff waits for the app to exit normally, snapshots the install and
// database, installs the verified package, checks the result and restarts.
// Any failure restores the previous build and restarts the old app.
func RunHandoff(h Handoff, deps helperDeps) Status {
	return runHandoff(h, deps)
}

func runHandoff(h Handoff, deps helperDeps) Status {
	if err := deps.waitExit(h.AppPID); err != nil {
		status := Status{Version: h.Version, Message: "等待应用退出失败: " + err.Error()}
		return withRestart(status, deps.restart, h.Restart)
	}
	restore, err := deps.prepare(h.InstallDir, h.DataDir)
	if err != nil {
		return failed(h, "更新前备份失败", errors.Join(err, restoreSafely(restore)), deps.restart, h.Restart)
	}
	if err := deps.install(h.Installer, h.InstallDir, h.Isolated); err != nil {
		return failed(h, "安装失败，已恢复旧版本", errors.Join(err, restoreSafely(restore)), deps.restart, h.Restart)
	}
	if err := deps.checkVersion(h.InstallDir, h.Version); err != nil {
		return failed(h, "版本核对失败，已恢复旧版本", errors.Join(err, restoreSafely(restore)), deps.restart, h.Restart)
	}
	if err := deps.verifyData(h.DataDir); err != nil {
		return failed(h, "用户数据校验失败，已恢复旧版本", errors.Join(err, restoreSafely(restore)), deps.restart, h.Restart)
	}
	return withRestart(Status{OK: true, Version: h.Version, Message: "已更新至 " + h.Version}, deps.restart, h.Restart)
}

func failed(h Handoff, message string, cause error, restart func(string) error, path string) Status {
	return withRestart(Status{Version: h.Version, Message: fmt.Sprintf("%s：%v", message, cause)}, restart, path)
}

func withRestart(status Status, restart func(string) error, path string) Status {
	if restart == nil {
		return status
	}
	if err := restart(path); err != nil {
		status.Message += fmt.Sprintf("；重启失败，请手动打开应用：%v", err)
	}
	return status
}

func restoreSafely(restore func() error) error {
	if restore == nil {
		return nil
	}
	return restore()
}
