package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

func claudeExecutable() (string, error) {
	if runtime.GOOS != "windows" {
		return exec.LookPath("claude")
	}
	if path, err := exec.LookPath("claude.exe"); err == nil {
		return path, nil
	}
	batch, err := exec.LookPath("claude.cmd")
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(batch), "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	return "", exec.ErrNotFound
}

var claudeBaseArgs = []string{"--safe-mode", "--setting-sources", "",
	"--settings", `{"disableAllHooks":true,"disableClaudeAiConnectors":true,"autoMemoryEnabled":false}`}

func claudeCommand(ctx context.Context, binary, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, append(slices.Clone(claudeBaseArgs), args...)...)
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = []string{"ATM_CLAUDE_TITLE_WORKER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_NON_ESSENTIAL_MODEL_CALLS=1", "ENABLE_TOOL_SEARCH=false"}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA",
			"HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TZ":
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "TEMP="+dir, "TMP="+dir, "TMPDIR="+dir)
	hideProcess(cmd)
	return cmd
}

func claudePrintArgs() []string {
	return []string{"--print", "--no-session-persistence", "--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`, "--tools", "", "--disable-slash-commands"}
}

func claudeAuth(ctx context.Context, binary, dir string) modelResult {
	cmd := claudeCommand(ctx, binary, dir, "auth", "status", "--json")
	var out limitedOutput
	cmd.Stdout = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return modelResult{status: "interrupted", message: "Claude Code 登录检查超时或已取消", failure: contextFailure(ctx.Err())}
	}
	var status struct {
		LoggedIn *bool  `json:"loggedIn"`
		Method   string `json:"authMethod"`
		Provider string `json:"apiProvider"`
	}
	if out.truncated || json.Unmarshal(out.Bytes(), &status) != nil || status.LoggedIn == nil {
		return modelResult{status: "failed", message: "此版本无法确认 Claude Code 的原生订阅登录"}
	}
	if err == nil && *status.LoggedIn && status.Provider == "firstParty" &&
		(status.Method == "claude.ai" || status.Method == "oauth_token") {
		return modelResult{}
	}
	return modelResult{status: "failed", message: "隔离模式下未检测到 Claude Code 的原生订阅登录，请在原版 CLI 检查登录",
		failure: failureClaudeLogin}
}

func claudeModels(ctx context.Context, dir string) ([]subscriptionModel, string, error) {
	binary, err := claudeExecutable()
	if err != nil {
		return nil, "", errors.New("找不到原生 Claude Code CLI")
	}
	if err := checkClaudeManagedSettings(); err != nil {
		return nil, "", err
	}
	args := append(claudePrintArgs(), "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
	items, err := readCLIStream(claudeCommand(ctx, binary, dir, args...), readClaudeCatalog)
	if err != nil {
		return nil, "", err
	}
	return items, claudeAuth(ctx, binary, dir).message, nil
}

func readClaudeCatalog(s cliStream) ([]subscriptionModel, error) {
	const id = "atm-catalog"
	request := map[string]any{"type": "control_request", "request_id": id,
		"request": map[string]string{"subtype": "initialize"}}
	if err := s.send(request); err != nil {
		return nil, err
	}
	reply, err := readCLIReply(s, func(frame claudeCatalogReply) bool {
		return frame.Type == "control_response" && frame.Response.ID == id
	})
	if err != nil || reply.Response.Subtype != "success" || reply.Response.Data.Models == nil {
		return nil, errCatalogProtocol
	}
	items := make([]subscriptionModel, 0, len(reply.Response.Data.Models))
	for _, entry := range reply.Response.Data.Models {
		if entry.SupportsEffort != nil && !*entry.SupportsEffort {
			entry.Efforts = nil
		}
		items = append(items, subscriptionModel{ID: entry.ID, Name: entry.Name,
			ResolvedModel: entry.Resolved, Efforts: entry.Efforts})
	}
	return items, nil
}

type claudeCatalogReply struct {
	Type     string `json:"type"`
	Response struct {
		ID      string `json:"request_id"`
		Subtype string `json:"subtype"`
		Data    struct {
			Models []struct {
				ID             string   `json:"value"`
				Name           string   `json:"displayName"`
				Resolved       string   `json:"resolvedModel"`
				SupportsEffort *bool    `json:"supportsEffort"`
				Efforts        []string `json:"supportedEffortLevels"`
			} `json:"models"`
		} `json:"response"`
	} `json:"response"`
}
