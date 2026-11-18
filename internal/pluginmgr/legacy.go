package pluginmgr

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	legacyPython = regexp.MustCompile(`(?i)^\s*(?:&\s+)?(?:"([^"]+)"|'([^']+)'|([^\s"';|&]+))\s+(?:-3\s+)?(?:-X\s+utf8\s+)?(?:"([^"]+)"|'([^']+)'|([^\s"';|&]+))(?:\s+hook)?\s*$`)
	pythonExe    = regexp.MustCompile(`^py(thon([23](\.[0-9]+)?)?)?(\.exe)?$`)
	legacyTable  = regexp.MustCompile(`^\s*\[\s*plugins\s*\.\s*["']oil-codex-title@oil-codex-title(?:-local)?["']\s*\]\s*(?:#.*)?$`)
	enabledLine  = regexp.MustCompile(`^(\s*enabled\s*=\s*)(true|false)(\s*(?:#.*)?)$`)
)

func legacyChanges(h host, base snapshot) ([]change, error) {
	var out []change
	switch h.id {
	case "codex", "claude":
		after, err := editJSON(base, func(root object) (bool, error) { return removeLegacyHook(root, h) })
		if err != nil {
			return nil, err
		}
		out = appendChanged(out, change{File: h.settings, Before: base, After: after})
		if h.id == "codex" {
			config := filepath.Join(h.dir, "config.toml")
			before, err := readSettings(config)
			if err != nil {
				return nil, err
			}
			out = appendChanged(out, change{File: config, Before: before, After: disableLegacyCodex(before)})
		}
	case "pi":
		old := filepath.Join(h.dir, "extensions", "auto-name")
		present, err := oldPiFolder(old)
		if err != nil || !present {
			return out, err
		}
		after, err := editJSON(base, func(root object) (bool, error) {
			return removeLegacyPiEntry(root, h.dir, old)
		})
		if err != nil {
			return nil, err
		}
		out = appendChanged(out, change{File: h.settings, Before: base, After: after})
		out = append(out, change{File: old, MoveTo: filepath.Join(h.dir, ".atm-disabled-auto-name")})
	}
	return out, nil
}

func appendChanged(changes []change, c change) []change {
	if c.MoveTo == "" && c.Before.equal(c.After) {
		return changes
	}
	return append(changes, c)
}

func removeLegacyHook(root object, h host) (bool, error) {
	hooks, err := nested(root, "hooks")
	if err != nil {
		return false, err
	}
	event := "Stop"
	if h.id == "claude" {
		event = "UserPromptSubmit"
	}
	edited, err := editCommands(hooks, event, h.id == "codex", func(cmd string) bool { return legacyCommand(h, cmd) }, nil)
	if err == nil && edited {
		err = setObject(root, "hooks", hooks)
	}
	return edited, err
}

func legacyCommand(h host, cmd string) bool {
	match := legacyPython.FindStringSubmatch(cmd)
	if match == nil {
		return false
	}
	exe := strings.ToLower(filepath.Base(filepath.FromSlash(strings.ReplaceAll(match[1]+match[2]+match[3], "\\", "/"))))
	if !pythonExe.MatchString(exe) {
		return false
	}
	script := match[4] + match[5] + match[6]
	if h.id == "claude" {
		return samePath(script, filepath.Join(h.dir, "session-namer", "namer.py"))
	}
	script = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(script, "\\", "/")))
	for _, market := range []string{"oil-codex-title-local", "oil-codex-title"} {
		root := filepath.Join(h.dir, "plugins", "cache", market, "oil-codex-title")
		rel, err := filepath.Rel(root, script)
		parts := strings.Split(rel, string(filepath.Separator))
		if err == nil && len(parts) == 3 && parts[0] != ".." && parts[1] == "scripts" && parts[2] == "oil_codex_title.py" {
			return true
		}
	}
	return false
}

func disableLegacyCodex(in snapshot) snapshot {
	out, _ := setLegacyCodex(in, false)
	return out
}

func onlyLegacyTable(in snapshot) string {
	found := ""
	for _, line := range strings.Split(string(in.Data), "\n") {
		if !legacyTable.MatchString(strings.TrimSpace(line)) {
			continue
		}
		if found != "" {
			return ""
		}
		found = "oil-codex-title@oil-codex-title"
		if strings.Contains(line, found+"-local") {
			found += "-local"
		}
	}
	return found
}

func setLegacyCodex(in snapshot, enabled bool) (snapshot, bool) {
	if !in.Exists {
		return in, false
	}
	lines := strings.Split(string(in.Data), "\n")
	inTable, inMultiline := false, ""
	value := "false"
	if enabled {
		value = "true"
	}
	found := false
	for i, line := range lines {
		plain := strings.TrimSpace(line)
		if inMultiline != "" {
			if strings.Count(line, inMultiline)%2 == 1 {
				inMultiline = ""
			}
			continue
		}
		if !strings.HasPrefix(plain, "#") {
			for _, delim := range []string{`"""`, "'''"} {
				if strings.Count(line, delim)%2 == 1 {
					inMultiline = delim
				}
			}
		}
		if strings.HasPrefix(plain, "[") {
			inTable = legacyTable.MatchString(plain)
		}
		if inTable {
			body := strings.TrimSuffix(line, "\r")
			if parts := enabledLine.FindStringSubmatch(body); parts != nil {
				lines[i] = parts[1] + value + parts[3] + line[len(body):]
				found = true
			}
		}
	}
	return snapshot{Exists: true, Data: []byte(strings.Join(lines, "\n"))}, found
}

func removeLegacyPiEntry(root object, home, folder string) (bool, error) {
	paths, err := get[[]string](root, "extensions")
	if err != nil {
		return false, err
	}
	var kept []string
	for _, path := range paths {
		check := path
		if !filepath.IsAbs(check) {
			check = filepath.Join(home, check)
		}
		if !samePath(check, folder) && !samePath(check, filepath.Join(folder, "index.js")) {
			kept = append(kept, path)
		}
	}
	if len(kept) == len(paths) {
		return false, nil
	}
	return true, setList(root, "extensions", kept)
}

func oldPiFolder(file string) (bool, error) {
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || isReparse(info) {
		return false, err
	}
	entry, err := readSettings(filepath.Join(file, "index.js"))
	if err != nil {
		return false, err
	}
	return bytes.Contains(entry.Data, []byte("pi.registerCommand('auto-name',")) &&
		bytes.Contains(entry.Data, []byte("from './core.js'")), nil
}

func moveExtension(from, to string) error {
	first, err := oldPiFolder(from)
	if err != nil {
		return err
	}
	second, err := oldPiFolder(to)
	if err != nil {
		return err
	}
	_, fromErr := os.Lstat(from)
	_, toErr := os.Lstat(to)
	switch {
	case second && errors.Is(fromErr, os.ErrNotExist):
		return nil
	case first && errors.Is(toErr, os.ErrNotExist):
		return os.Rename(from, to)
	default:
		return errors.New("旧 Pi 扩展路径有冲突，已保留现有内容")
	}
}
