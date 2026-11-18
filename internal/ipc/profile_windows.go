package ipc

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func Profile() (dir, scope string, err error) {
	if override := os.Getenv("ATM_DATA_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", "", fmt.Errorf("ATM_DATA_DIR 必须为绝对路径")
		}
		dir = filepath.Clean(override)
		scope = fmt.Sprintf(".%x", sha256.Sum256([]byte(strings.ToLower(dir))))[:17]
		return dir, scope, nil
	}
	dir = os.Getenv("LOCALAPPDATA")
	if dir == "" {
		return "", "", fmt.Errorf("无法确定 LOCALAPPDATA")
	}
	return filepath.Join(dir, "AgentTitleManager"), "", nil
}

func identity(scope string) (string, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", "", err
	}
	sid := user.User.Sid.String()
	return `\\.\pipe\AgentTitleManager.v1.` + sid + scope, "D:P(A;;GA;;;" + sid + ")", nil
}
