package pluginmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		quotedNode, err := quoteCommandPath(node)
		if err != nil {
			return "", err
		}
		quotedEntry, err := quoteCommandPath(entry)
		if err != nil {
			return "", err
		}
		result = quotedNode + " " + quotedEntry
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
