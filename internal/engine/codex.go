package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) runCodex(ctx context.Context, s Settings, policy string, input []byte, start func() error) modelResult {
	binary, err := codexExecutable()
	if err != nil {
		return modelResult{status: "failed", message: "找不到原生 Codex CLI", confirmed: true}
	}
	dir, err := os.MkdirTemp(e.dir, "codex-exec-")
	if err != nil {
		return modelResult{status: "failed", message: "无法创建临时命名目录"}
	}
	defer os.RemoveAll(dir)
	if auth := codexAuth(ctx, binary, dir); auth.status != "" {
		return auth
	}
	args, err := codexArgs(dir, s, policy)
	if err != nil {
		return modelResult{status: "failed", message: "无法准备临时命名规则"}
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := codexCommand(child, binary, dir, args...)
	cmd.Stdin = bytes.NewReader(input)
	out := codexOutput{cancel: cancel}
	cmd.Stdout = &out
	if err = start(); err != nil {
		return modelResult{status: "interrupted"}
	}
	err = cmd.Run()
	result := parseCodex(out.Bytes(), out.truncated, err, child.Err())
	if result.status == "interrupted" {
		// 子进程也可能因连接结果不明而在内部停止。仅父上下文能确认取消或超时。
		result.failure = contextFailure(ctx.Err())
	}
	return result
}

func codexExecutable() (string, error) {
	if runtime.GOOS != "windows" {
		return exec.LookPath("codex")
	}
	if path, err := exec.LookPath("codex.exe"); err == nil {
		return path, nil
	}
	batch, err := exec.LookPath("codex.cmd")
	if err != nil {
		return "", err
	}
	arch, target := "x64", "x86_64-pc-windows-msvc"
	if runtime.GOARCH == "arm64" {
		arch, target = "arm64", "aarch64-pc-windows-msvc"
	}
	node := filepath.Join(filepath.Dir(batch), "node_modules", "@openai")
	pkg := filepath.Join(node, "codex")
	if resolved, err := filepath.EvalSymlinks(pkg); err == nil {
		pkg = resolved
	}
	name := "codex-win32-" + arch
	roots := []string{pkg, filepath.Join(pkg, "node_modules", "@openai", name), filepath.Join(node, name)}
	for dir := filepath.Dir(pkg); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		roots = append(roots, filepath.Join(dir, name))
	}
	for _, root := range roots {
		for _, folder := range []string{"bin", "codex"} {
			path := filepath.Join(root, "vendor", target, folder, "codex.exe")
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

const fileAuth = `cli_auth_credentials_store="file"`

func codexCommand(ctx context.Context, binary, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CODEX_AUTH_JSON",
			"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_APP_TOOLS_PIPE_PATH", "CODEX_MANAGED_BY_APP_SERVER":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "OIL_CODEX_TITLE_WORKER=1")
	hideProcess(cmd)
	return cmd
}

func codexAuth(ctx context.Context, binary, dir string) modelResult {
	cmd := codexCommand(ctx, binary, dir, "login", "status", "-c", fileAuth)
	var out limitedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.ToLower(out.String())
	switch {
	case err == nil && strings.Contains(text, "logged in using chatgpt"):
		return modelResult{}
	case strings.Contains(text, "not logged in") || strings.Contains(text, "logged in using an api key"):
		return modelResult{status: "failed", message: "请先在 Codex CLI 登录 ChatGPT 订阅", confirmed: true, failure: failureCodexLogin}
	case ctx.Err() != nil:
		return modelResult{status: "interrupted", message: "Codex 登录检查超时或已取消", failure: contextFailure(ctx.Err())}
	default:
		return modelResult{status: "failed", message: "无法确认 Codex CLI 已登录 ChatGPT 订阅"}
	}
}

func codexArgs(dir string, s Settings, policy string) ([]string, error) {
	policyPath, schemaPath := filepath.Join(dir, "naming.md"), filepath.Join(dir, "schema.json")
	if err := os.WriteFile(policyPath, []byte(policy), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(schemaPath, []byte(decisionSchema), 0600); err != nil {
		return nil, err
	}
	args := []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
		"--sandbox", "read-only", "-C", dir, "--color", "never", "-m", s.CodexModel,
		"-c", fileAuth, "-c", `model_provider="openai"`, "-c", `model_reasoning_effort="low"`,
		"-c", "project_doc_max_bytes=0", "-c", "skills.max_context_tokens=1",
		"-c", "agents.enabled=false", "-c", `web_search="disabled"`,
		"-c", "apps._default.enabled=false", "-c", "model_instructions_file=" + strconv.Quote(policyPath)}
	for _, feature := range []string{"hooks", "shell_tool", "plugins", "apps", "multi_agent", "multi_agent_v2", "image_generation", "skill_mcp_dependency_install"} {
		args = append(args, "--disable", feature)
	}
	if s.Fast {
		args = append(args, "-c", `service_tier="priority"`)
	}
	return append(args, "--output-schema", schemaPath, "--json", "-"), nil
}

type limitedOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (o *limitedOutput) Write(data []byte) (int, error) {
	space := 256<<10 - o.buffer.Len()
	if len(data) > space {
		o.truncated = true
	}
	o.buffer.Write(data[:min(len(data), space)])
	return len(data), nil
}

func (o *limitedOutput) Bytes() []byte  { return o.buffer.Bytes() }
func (o *limitedOutput) String() string { return o.buffer.String() }

var errOutputLimit = errors.New("命名服务响应超过大小限制")

type codexOutput struct {
	limitedOutput
	read   int
	cancel func()
}

func (o *codexOutput) Write(data []byte) (int, error) {
	n, err := o.limitedOutput.Write(data)
	for {
		line, _, full := bytes.Cut(o.Bytes()[o.read:], []byte{'\n'})
		if !full {
			return n, err
		}
		o.read += len(line) + 1
		var event codexEvent
		if json.Unmarshal(line, &event) == nil && event.Type == "error" && ambiguousReconnect(event.Message) {
			o.cancel()
		}
	}
}

func ambiguousReconnect(message string) bool {
	if !strings.HasPrefix(message, "Reconnecting... ") {
		return false
	}
	_, cause, _ := strings.Cut(message, " (")
	return !isHTTPRefusal(cause)
}
