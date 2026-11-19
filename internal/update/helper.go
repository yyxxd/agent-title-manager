package update

import "fmt"

// helperDeps holds the platform operations so the handoff sequence can be
// tested without launching real installers or processes.
type helperDeps struct {
	verify       func() error
	waitExit     func(uint32) error
	prepare      func(installDir, dataDir string) (restore func() error, err error)
	install      func(installer, installDir string, isolated bool) error
	checkVersion func(installDir, version string) error
	verifyData   func(dataDir string) error
	verifyVersion func(installDir, version string) error
	restart      func(path string) error
}

// RunHandoff re-verifies the untrusted handoff, waits for a normal app exit,
// snapshots known install files and the database, installs, then validates.
// Any failure is reported honestly; the old app is restarted only when the
// previous build is confirmed restored.
func RunHandoff(h Handoff, deps helperDeps) Status { return runHandoff(h, deps) }

func runHandoff(h Handoff, deps helperDeps) Status {
	if err := deps.verify(); err != nil {
		return Status{Version: h.Version, Message: "交接内容校验失败，未安装: " + err.Error()}
	}
	if err := deps.waitExit(h.AppPID); err != nil {
		return Status{Version: h.Version, Message: "等待应用退出失败，未安装: " + err.Error()}
	}
	restore, err := deps.prepare(h.InstallDir, h.DataDir)
	if err != nil {
		return withRestart(Status{Version: h.Version,
			Message: fmt.Sprintf("更新前备份失败，未安装；备份保留供排查：%v", err)}, deps.restart, h.Restart)
	}
	if err := deps.install(h.Installer, h.InstallDir, h.Isolated); err != nil {
		return recover(h, deps, restore, "安装失败", err)
	}
	if err := deps.checkVersion(h.InstallDir, h.Version); err != nil {
		return recover(h, deps, restore, "安装后版本核对失败", err)
	}
	if err := deps.verifyData(h.DataDir); err != nil {
		return recover(h, deps, restore, "用户数据校验失败", err)
	}
	if deps.verifyVersion != nil {
		if err := deps.verifyVersion(h.InstallDir, h.Version); err != nil {
			return recover(h, deps, restore, "安装登记版本核对失败", err)
		}
	}
	return withRestart(Status{OK: true, Version: h.Version, Message: "已更新至 " + h.Version}, deps.restart, h.Restart)
}

// recover restores only the known install files and confirms the previous
// build before restarting. A failed rollback never claims success.
func recover(h Handoff, deps helperDeps, restore func() error, reason string, cause error) Status {
	message := fmt.Sprintf("%s：%v", reason, cause)
	if restore == nil {
		return Status{Version: h.Version, Message: message + "；无可用备份，未重启"}
	}
	if err := restore(); err != nil {
		return Status{Version: h.Version, Message: message + fmt.Sprintf("；恢复旧版本失败: %v，未重启", err)}
	}
	if err := deps.checkVersion(h.InstallDir, h.PreviousVersion); err != nil {
		return Status{Version: h.Version, Message: message + fmt.Sprintf("；旧版本核对失败: %v，未重启", err)}
	}
	return withRestart(Status{Version: h.PreviousVersion, Message: message + "；旧版本已恢复"}, deps.restart, h.Restart)
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
