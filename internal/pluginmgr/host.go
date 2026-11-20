package pluginmgr

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type host struct {
	id       string
	dir      string
	settings string
}

func resolveHost(id string) (host, error) {
	env, relative, settings := "", "", "settings.json"
	switch id {
	case "codex":
		env, relative, settings = "CODEX_HOME", ".codex", "hooks.json"
	case "claude":
		env, relative = "CLAUDE_CONFIG_DIR", ".claude"
	case "pi":
		env, relative = "PI_CODING_AGENT_DIR", filepath.Join(".pi", "agent")
	case "antigravity":
		env, relative, settings = "ANTIGRAVITY_CONFIG_DIR", filepath.Join(".gemini", "config"), "hooks.json"
	default:
		return host{}, errors.New("宿主不受支持")
	}
	dir := os.Getenv(env)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return host{}, err
		}
		dir = filepath.Join(home, relative)
	}
	if !filepath.IsAbs(dir) {
		return host{}, fmt.Errorf("%s 须为绝对路径", env)
	}
	dir = filepath.Clean(dir)
	return host{id, dir, filepath.Join(dir, settings)}, nil
}

type event struct {
	name, arg string
	async     bool
	timeout   int
}

func events(agent string) []event {
	switch agent {
	case "codex":
		return []event{{"SessionStart", "start", true, 120}, {"Stop", "stop", true, 120}}
	case "claude":
		return []event{{"SessionStart", "start", false, 3}, {"UserPromptSubmit", "prompt", false, 3}, {"Stop", "stop", true, 120}}
	case "antigravity":
		return []event{{"SessionStart", "start", false, 10}, {"PreInvocation", "prompt", false, 10}, {"Stop", "stop", false, 10}}
	}
	return nil
}

func (m *Manager) hostEntry(id string, rel release) (string, error) {
	dir := m.packageDir(rel)
	entry := filepath.Join(dir, filepath.FromSlash(rel.Manifest.Entry))
	result := entry
	if id != "pi" {
		node, err := m.findExecutable("node.exe", true)
		if err != nil {
			return "", err
		}
		result, err = hookEntry(id, node, entry)
		if err != nil {
			return "", err
		}
	}
	if id == "codex" && !newerVersion("0.2.1", rel.Manifest.Version) {
		result += " " + codexHookRegistrationPrefix + rand.Text()
	}
	return result, m.writeRuntime(dir)
}

func (m *Manager) writeRuntime(dir string) error {
	bridge, err := m.findExecutable("atm-bridge.exe", false)
	if err != nil {
		return err
	}
	runtime := map[string]string{"bridgePath": bridge}
	if codex, err := exec.LookPath("codex.exe"); err == nil && filepath.IsAbs(codex) {
		runtime["codexPath"] = codex
	}
	wire, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, runtimeName), wire, 0600)
}

func hookEntry(id, node, entry string) (string, error) {
	if id == "antigravity" {
		if !filepath.IsAbs(node) || !filepath.IsAbs(entry) {
			return "", errors.New("Antigravity Hook: executable and script paths must be absolute")
		}
		return platformHookEntry(id, node, entry, strconv.Quote(node)+" "+strconv.Quote(entry))
	}
	quotedNode, err := quoteCommandPath(node)
	if err != nil {
		return "", err
	}
	quotedEntry, err := quoteCommandPath(entry)
	if err != nil {
		return "", err
	}
	return platformHookEntry(id, node, entry, quotedNode+" "+quotedEntry)
}

func quoteCommandPath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\"%!$`\r\n\x00") {
		return "", errors.New("命令路径包含 Windows Shell 无法安全引用的字符")
	}
	return "\"" + path + "\"", nil
}

func (m *Manager) findExecutable(name string, usePath bool) (string, error) {
	dirs := []string{m.bundled, filepath.Dir(m.bundled)}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	if usePath {
		if path, err := exec.LookPath(name); err == nil && filepath.IsAbs(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 %s", name)
}

func registration(h host, base snapshot, entry string, adding bool) (change, error) {
	out, err := editJSON(base, func(root object) (bool, error) {
		if h.id == "pi" {
			return editPi(root, entry, adding)
		}
		if h.id == "antigravity" {
			return editAntigravity(root, entry, adding)
		}
		return editHostHooks(root, h.id, entry, adding)
	})
	return change{File: h.settings, Before: base, After: out}, err
}

func editPi(root object, entry string, adding bool) (bool, error) {
	paths, err := get[[]string](root, "extensions")
	if err != nil {
		return false, err
	}
	var kept []string
	found := false
	for _, path := range paths {
		if samePath(path, entry) {
			found = true
		} else {
			kept = append(kept, path)
		}
	}
	if adding == found {
		return false, nil
	}
	if adding {
		kept = append(kept, entry)
	}
	return true, setList(root, "extensions", kept)
}

const antigravityLauncher = ` -NoProfile -NonInteractive -Command "& `

var oldAntigravityCommand = regexp.MustCompile(`(?i)^"[^"]*[\\/]node(?:\.exe)?" "[^"]*[\\/]packages[\\/]pkg-[0-9]{8}-[0-9]+[\\/]index\.mjs" (start|prompt|stop)$`)
var encodedAntigravityCommand = regexp.MustCompile(`(?i)^[A-Z]:/[\pL\pN_./-]+/WindowsPowerShell/v1\.0/powershell\.exe` + regexp.QuoteMeta(antigravityLauncher) +
	regexp.QuoteMeta(`([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('`) + `[A-Za-z0-9+/]+={0,2}` +
	regexp.QuoteMeta(`'))) ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('`) + `[A-Za-z0-9+/]+={0,2}` + regexp.QuoteMeta(`')))"`) + ` (start|prompt|stop)$`)

func editAntigravity(root object, entry string, adding bool) (bool, error) {
	const groupName = "agent-title-manager"
	groups, err := nested(root, groupName)
	if err != nil {
		return false, err
	}
	changed := false
	for _, e := range events("antigravity") {
		command := entry + " " + e.arg
		items, err := get[[]json.RawMessage](groups, e.name)
		if err != nil {
			return false, err
		}
		kept := make([]json.RawMessage, 0, len(items))
		found := false
		for _, raw := range items {
			var h map[string]any
			if json.Unmarshal(raw, &h) != nil {
				return false, errors.New("Antigravity Hook 配置无法解析")
			}
			value, _ := h["command"].(string)
			if h["type"] == "command" && (value == command || oldAntigravityCommand.MatchString(value) || encodedAntigravityCommand.MatchString(value)) {
				if adding && !found && value == command {
					found = true
				} else {
					changed = true
					continue
				}
			}
			kept = append(kept, raw)
		}
		if adding && !found {
			raw, _ := json.Marshal(map[string]any{"type": "command", "command": command, "timeout": 10})
			kept = append(kept, raw)
			changed = true
		}
		if len(kept) == 0 {
			delete(groups, e.name)
		} else if err := setList(groups, e.name, kept); err != nil {
			return false, err
		}
	}
	if len(groups) == 0 {
		delete(root, groupName)
	} else if err := setObject(root, groupName, groups); err != nil {
		return false, err
	}
	return changed, nil
}

func editHostHooks(root object, id, entry string, adding bool) (bool, error) {
	hooks, err := nested(root, "hooks")
	if err != nil {
		return false, err
	}
	changed := false
	for _, e := range events(id) {
		command := entry + " " + e.arg
		add := func() (json.RawMessage, error) {
			value := map[string]any{"type": "command", "command": command, "async": e.async, "timeout": e.timeout}
			return json.Marshal(value)
		}
		if !adding {
			add = nil
		}
		edited, err := editCommands(hooks, e.name, id == "codex", func(s string) bool { return s == command }, add)
		if err != nil {
			return false, err
		}
		changed = changed || edited
	}
	if changed {
		err = setObject(root, "hooks", hooks)
	}
	return changed, err
}

func samePath(a, b string) bool {
	a = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(a, "\\", "/")))
	b = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(b, "\\", "/")))
	return strings.EqualFold(a, b)
}
