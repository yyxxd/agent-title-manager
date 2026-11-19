package desktop

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\AgentTitleManager`

// detectInstallDir prefers an explicit isolated override for acceptance runs,
// then falls back to the registry key written by the NSIS installer.
func detectInstallDir() (string, error) {
	if override := os.Getenv("ATM_INSTALL_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("ATM_INSTALL_DIR 必须为绝对路径")
		}
		return filepath.Clean(override), nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	if err != nil || dir == "" {
		return "", errors.New("无法确定安装目录，请在安装目录内启动更新")
	}
	return dir, nil
}

func helperProcAttr() *windows.SysProcAttr {
	return &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
}
