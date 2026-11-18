package pluginmgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
)

var restoreConflict = errors.New("旧插件设置已改变，请先解决设置冲突；当前内容已保留")

func mergeLegacy(undo change, current snapshot) (change, error) {
	if !current.Exists || !undo.Before.Exists || !undo.After.Exists {
		return change{}, restoreConflict
	}
	if filepath.Base(undo.File) == "config.toml" {
		table := onlyLegacyTable(undo.Before)
		if table == "" || onlyLegacyTable(current) != table {
			return change{}, restoreConflict
		}
		if next, found := setLegacyCodex(current, true); found {
			undo.Before, undo.After = current, next
			return undo, nil
		}
		return change{}, restoreConflict
	}
	merged, ok := restoreObject(undo.After.Data, undo.Before.Data, current.Data)
	if !ok {
		return change{}, restoreConflict
	}
	var padded bytes.Buffer
	if err := json.Indent(&padded, merged, "", "  "); err != nil {
		return change{}, restoreConflict
	}
	undo.Before = current
	undo.After.Data = append(padded.Bytes(), '\n')
	return undo, nil
}

func restoreObject(before, after, actual json.RawMessage) (json.RawMessage, bool) {
	if sameJSON(before, after) || sameJSON(before, actual) {
		return actual, true
	}
	if sameJSON(after, actual) {
		return before, true
	}
	var a, b, c object
	if !asObject(before, &a) || !asObject(after, &b) || !asObject(actual, &c) {
		return nil, false
	}
	for key := range b {
		if _, found := a[key]; !found {
			a[key] = nil
		}
	}
	for key, raw := range a {
		value, ok := restoreObject(raw, b[key], c[key])
		if !ok {
			return nil, false
		}
		if value == nil {
			delete(c, key)
		} else {
			c[key] = value
		}
	}
	out, err := json.Marshal(c)
	return out, err == nil
}

func asObject(raw json.RawMessage, dst *object) bool {
	*dst = object{}
	return raw == nil || json.Unmarshal(raw, dst) == nil && *dst != nil
}

func sameJSON(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var first, second bytes.Buffer
	return json.Compact(&first, a) == nil && json.Compact(&second, b) == nil && bytes.Equal(first.Bytes(), second.Bytes())
}
