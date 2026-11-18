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

func editCommands(events object, event string, match func(string) bool, add func() (json.RawMessage, error)) (bool, error) {
	groups, err := get[[]json.RawMessage](events, event)
	if err != nil {
		return false, err
	}
	var kept []json.RawMessage
	found := false
	for _, group := range groups {
		next, matched, err := editGroup(group, match, add != nil)
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

func editGroup(raw json.RawMessage, match func(string) bool, keep bool) (json.RawMessage, bool, error) {
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
		var h struct{ Type, Command, CommandWindows string }
		if json.Unmarshal(item, &h) != nil {
			return nil, false, errors.New("宿主 Hook 命令无法解析")
		}
		if h.Type == "command" && (match(h.Command) || match(h.CommandWindows)) {
			found = true
			if !keep {
				continue
			}
		}
		kept = append(kept, item)
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
