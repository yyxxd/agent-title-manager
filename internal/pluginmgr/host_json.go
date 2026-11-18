package pluginmgr

import (
	"encoding/json"
	"errors"
)

type object = map[string]json.RawMessage

func editJSON(in snapshot, edit func(object) (bool, error)) (snapshot, error) {
	root := object{}
	if in.Exists && (json.Unmarshal(in.Data, &root) != nil || root == nil) {
		return in, errors.New("宿主 JSON 设置无法解析")
	}
	changed, err := edit(root)
	if err != nil || !changed {
		return in, err
	}
	wire, err := json.MarshalIndent(root, "", "  ")
	return snapshot{Exists: true, Data: append(wire, '\n')}, err
}

func get[T any](source object, key string) (T, error) {
	var value T
	if wire, ok := source[key]; ok && (string(wire) == "null" || json.Unmarshal(wire, &value) != nil) {
		return value, errors.New("宿主设置结构不受支持")
	}
	return value, nil
}

func nested(source object, key string) (object, error) {
	value, err := get[object](source, key)
	if value == nil {
		value = object{}
	}
	return value, err
}

func setValue(source object, key string, v any) error {
	raw, err := json.Marshal(v)
	if err == nil {
		source[key] = raw
	}
	return err
}

func setList[T any](source object, key string, v []T) error {
	if len(v) == 0 {
		delete(source, key)
		return nil
	}
	return setValue(source, key, v)
}

func setObject(source object, key string, v object) error {
	if len(v) == 0 {
		delete(source, key)
		return nil
	}
	return setValue(source, key, v)
}

func editCommands(events object, event string, codex bool, match func(string) bool, add func() (json.RawMessage, error)) (bool, error) {
	groups, err := get[[]json.RawMessage](events, event)
	if err != nil {
		return false, err
	}
	var kept []json.RawMessage
	found := false
	for _, group := range groups {
		next, matched, err := editGroup(group, codex, match, add != nil)
		if err != nil {
			return false, err
		}
		found = found || matched
		if next != nil {
			kept = append(kept, next)
		}
	}
	if (add != nil) == found {
		return false, nil
	}
	if add != nil {
		hook, err := add()
		if err != nil {
			return false, err
		}
		group, err := json.Marshal(map[string][]json.RawMessage{"hooks": {hook}})
		if err != nil {
			return false, err
		}
		kept = append(kept, group)
	}
	return true, setList(events, event, kept)
}

func editGroup(raw json.RawMessage, codex bool, match func(string) bool, keep bool) (json.RawMessage, bool, error) {
	var group object
	if json.Unmarshal(raw, &group) != nil || group == nil {
		return nil, false, errors.New("宿主 Hook 项无法解析")
	}
	items, err := get[[]json.RawMessage](group, "hooks")
	if err != nil {
		return nil, false, err
	}
	found := false
	var kept []json.RawMessage
	for _, item := range items {
		next, matched, err := editCommand(item, codex, match, keep)
		if err != nil {
			return nil, false, err
		}
		found = found || matched
		if next != nil {
			kept = append(kept, next)
		}
	}
	switch {
	case !found || keep:
		return raw, found, nil
	case len(kept) == 0:
		return nil, true, nil
	default:
		if err := setList(group, "hooks", kept); err != nil {
			return nil, false, err
		}
		out, err := json.Marshal(group)
		return out, true, err
	}
}

type hookCommand struct {
	Type, Command  string
	CommandWindows *string `json:"commandWindows"`
	WindowsAlias   *string `json:"command_windows"`
}

const inactiveWindowsCommand = "exit /b 0"

func editCommand(raw json.RawMessage, codex bool, match func(string) bool, keep bool) (json.RawMessage, bool, error) {
	var h hookCommand
	if json.Unmarshal(raw, &h) != nil {
		return nil, false, errors.New("宿主 Hook 命令无法解析")
	}
	windows, field := h.CommandWindows, "commandWindows"
	if codex && h.WindowsAlias != nil {
		if windows != nil {
			return nil, false, errors.New("Codex Hook 同时配置了两个 Windows 命令字段")
		}
		windows, field = h.WindowsAlias, "command_windows"
	}
	effective := h.Command
	if codex && windows != nil {
		effective = *windows
	}
	if h.Type != "command" || !match(effective) {
		return raw, false, nil
	}
	if keep {
		return raw, true, nil
	}
	if !codex || windows == nil || h.Command == "" || match(h.Command) {
		return nil, true, nil
	}
	// Preserve the non-Windows command without letting it become a Windows fallback.
	var fields object
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, err
	}
	if err := setValue(fields, field, inactiveWindowsCommand); err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(fields)
	return out, true, err
}
