package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func makePrompt(p submission) ([]byte, error) {
	if p.NativeOnly {
		return nil, nil
	}
	turns, hasUser := recentTurns(p.Turns)
	goal := truncateText(p.OriginalGoal, 800)
	if !hasUser && goal == "" {
		return nil, nil
	}
	hint, _ := inspectProject(p.Project)
	return json.Marshal(struct {
		Turns        []turn `json:"recent_turns"`
		OriginalGoal string `json:"original_goal,omitempty"`
		CurrentTitle string `json:"current_title"`
		ProjectHint  string `json:"project_hint,omitempty"`
		Suggestion   bool   `json:"title_suggestion,omitempty"`
	}{turns, goal, p.CurrentTitle, hint, p.Suggestion})
}

func recentTurns(input []turn) ([]turn, bool) {
	turns := make([]turn, 0, min(10, len(input)))
	budget := 6000
	hasUser := false
	for i := len(input) - 1; i >= 0 && len(turns) < 10 && budget > 0; i-- {
		t := input[i]
		text := truncateText(t.Text, min(1600, budget))
		if text == "" {
			continue
		}
		budget -= utf8.RuneCountInString(text)
		turns = append(turns, turn{Role: t.Role, Text: text})
		hasUser = hasUser || t.Role == "user"
	}
	slices.Reverse(turns)
	return turns, hasUser
}

func truncateText(text string, n int) string {
	text = strings.TrimSpace(text)
	for start := range text {
		if n == 0 {
			return text[:start]
		}
		n--
	}
	return text
}

func inspectProject(project string) (hint, scope string) {
	p := strings.ReplaceAll(strings.TrimSpace(project), "\\", "/")
	if p == "" || strings.HasPrefix(p, "//./") {
		return "", ""
	}
	if prefix, rest, ok := strings.Cut(p, "//?/"); ok && prefix == "" {
		p = rest
		if len(p) >= 4 && strings.EqualFold(p[:4], "UNC/") {
			p = "//" + p[4:]
		}
	}
	unc := strings.HasPrefix(p, "//")
	drive := len(p) >= 3 && isDriveLetter(p[0]) && p[1:3] == ":/"
	clean := path.Clean(p)
	if unc {
		parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
		if len(parts) < 3 {
			return "", ""
		}
		clean = "/" + clean
	}
	if drive && (len(clean) < 4 || clean[1:3] != ":/") {
		return "", ""
	}
	if strings.HasPrefix(p, "/") || drive {
		key := clean
		if unc || drive {
			key = strings.ToLower(key)
		}
		scope = hashText([]byte(key))
	}
	leaf := path.Base(clean)
	if genericProjectLeaf(leaf, path.Base(path.Dir(clean))) {
		return "", scope
	}
	return leaf, scope
}

func isDriveLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func genericProjectLeaf(leaf, parent string) bool {
	switch strings.ToLower(leaf) {
	case "", ".", "..", "/", "users", "home", "desktop", "documents", "downloads", "tmp", "temp", "project", "projects":
		return true
	}
	if strings.EqualFold(parent, "users") || strings.EqualFold(parent, "home") || utf8.RuneCountInString(leaf) > 64 {
		return true
	}
	return strings.ContainsFunc(leaf, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == ' ' || r == '.' || r == '-' || r == '_')
	})
}

func hashText(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
