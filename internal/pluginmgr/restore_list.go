package pluginmgr

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
)

type raw = json.RawMessage
type sameItem func(raw, raw) bool

func (m legacyRestore) list(before, after, actual raw, key string) (raw, bool) {
	var original, taken, current []raw
	if !decodeList(before, &original) || !decodeList(after, &taken) || !decodeList(actual, &current) {
		return nil, false
	}
	match := hookIdentity
	if key == "extensions" {
		match = m.extensionIdentity
	}
	pairs, ok := alignOriginal(original, taken, match)
	if !ok {
		return nil, false
	}
	for index, afterIndex := range pairs {
		if afterIndex < 0 || sameJSON(original[index], taken[afterIndex]) {
			continue
		}
		at := uniqueItem(current, taken[afterIndex], match)
		if at < 0 {
			return nil, false
		}
		item, ok := m.value(original[index], taken[afterIndex], current[at], "")
		if !ok {
			return nil, false
		}
		current[at] = item
	}
	for index, afterIndex := range pairs {
		if afterIndex >= 0 {
			continue
		}
		at := uniqueItem(current, original[index], match)
		if at == -2 || (at >= 0 && !sameJSON(current[at], original[index])) {
			return nil, false
		}
		if at < 0 {
			at = insertionPoint(current, original, index, match)
			current = slices.Insert(current, at, original[index])
		}
	}
	out, err := json.Marshal(current)
	return out, err == nil
}

func decodeList(data raw, dest *[]raw) bool {
	return data == nil || (isArray(data) && json.Unmarshal(data, dest) == nil)
}

func isArray(data raw) bool { return bytes.HasPrefix(bytes.TrimSpace(data), []byte{'['}) }

func alignOriginal(original, taken []raw, match sameItem) ([]int, bool) {
	pairs := make([]int, len(original))
	for i := range pairs {
		pairs[i] = -1
	}
	start := 0
	for i, item := range taken {
		at := slices.IndexFunc(original[start:], func(candidate raw) bool { return sameJSON(candidate, item) })
		if at < 0 {
			at = uniqueItem(original[start:], item, match)
		}
		if at < 0 {
			return nil, false
		}
		pairs[start+at] = i
		start += at + 1
	}
	return pairs, true
}

func uniqueItem(items []raw, target raw, match sameItem) int {
	found := -1
	for i, item := range items {
		if sameJSON(item, target) || match(item, target) {
			if found >= 0 {
				return -2
			}
			found = i
		}
	}
	return found
}

func insertionPoint(current, original []raw, index int, match sameItem) int {
	for _, following := range original[index+1:] {
		if at := uniqueItem(current, following, match); at >= 0 {
			return at
		}
	}
	for i := index - 1; i >= 0; i-- {
		if at := uniqueItem(current, original[i], match); at >= 0 {
			return at + 1
		}
	}
	return len(current)
}

func (m legacyRestore) extensionIdentity(a, b raw) bool {
	var left, right string
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil || left == "" || right == "" {
		return false
	}
	if !filepath.IsAbs(left) {
		left = filepath.Join(m.home, left)
	}
	if !filepath.IsAbs(right) {
		right = filepath.Join(m.home, right)
	}
	return samePath(left, right)
}

func hookIdentity(a, b raw) bool {
	var left, right object
	if !asObject(a, &left) || !asObject(b, &right) {
		return false
	}
	if left["hooks"] == nil && right["hooks"] == nil {
		return commandIdentity(left, right)
	}
	if !sameJSON(left["matcher"], right["matcher"]) {
		return false
	}
	l, lErr := get[[]raw](left, "hooks")
	r, rErr := get[[]raw](right, "hooks")
	if lErr != nil || rErr != nil {
		return false
	}
	for _, candidate := range l {
		if uniqueItem(r, candidate, hookIdentity) >= 0 {
			return true
		}
	}
	return false
}

func commandIdentity(a, b object) bool {
	if !sameJSON(a["type"], b["type"]) {
		return false
	}
	for _, key := range []string{"command", "commandWindows", "command_windows"} {
		if a[key] != nil && string(a[key]) != "\"\"" && string(a[key]) != `"exit /b 0"` && sameJSON(a[key], b[key]) {
			return true
		}
	}
	return false
}
