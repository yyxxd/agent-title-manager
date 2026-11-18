package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var errClaudePolicy = errors.New("检测到 Claude Code 托管配置或无法确认其状态，无法隔离托管 Hook 和插件")

func checkClaudeManagedSettings() error {
	base := "/etc/claude-code"
	switch runtime.GOOS {
	case "windows":
		base = filepath.Join(os.Getenv("ProgramFiles"), "ClaudeCode")
		if err := checkClaudePolicyRegistry(); err != nil {
			return err
		}
	case "darwin":
		base = "/Library/Application Support/ClaudeCode"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return errClaudePolicy
	}
	for _, path := range []string{filepath.Join(base, "managed-settings.json"), filepath.Join(base, "managed-settings.d"),
		filepath.Join(base, "managed-mcp.json"), filepath.Join(home, ".claude", "remote-settings.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			return errClaudePolicy
		}
	}
	return nil
}
