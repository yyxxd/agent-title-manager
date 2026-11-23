package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrSkipped       = errors.New("Orca 同步已跳过")
	ErrUnavailable   = errors.New("无法读取 Orca 会话")
	ErrUnconfirmed   = errors.New("无法确认 Orca 标题写入")
	ErrNativePending = errors.New("暂时无法核验原生会话标题")
	ErrNativeStale   = errors.New("原生标题已改变")
	sessionID        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	lock             sync.Mutex
)

const idPrefix = "orca_session_id:"

type runCLI func(context.Context, ...string) ([]byte, error)
type NativeTitleReader func(ctx context.Context, agent, sessionID, nativeHandle string) (string, error)
type preflight func(context.Context, place) error

type place struct {
	ID        string  `json:"orcaSessionId"`
	Agent     string  `json:"agent"`
	Kind      string  `json:"kind"`
	HostID    string  `json:"hostId"`
	Repo      string  `json:"repo"`
	Title     *string `json:"title"`
	PaneKey   string  `json:"paneKey"`
	Handle    string  `json:"handle"`
	PaneTitle string  `json:"-"`
}

type syncChecks struct {
	validate NativeHandleValidator
	read     NativeTitleReader
	readChat NativeTitleReader
	origin   func(context.Context, string, string, string) string
}

func syncWithChecks(ctx context.Context, run runCLI, id, expected, title, agent, session, handle string, checks syncChecks) error {
	if checks.read == nil {
		return ErrNativePending
	}
	return syncTitle(ctx, run, id, expected, title, func(ctx context.Context, prior place) error {
		delays := []time.Duration{0}
		if agent == "antigravity" {
			delays = []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond}
		}
		at, bound := discover(ctx, run, checks.validate, agent, session, handle, prior.ID, prior.Kind, delays)
		if !bound || at.Title == nil {
			return fmt.Errorf("%w: 无法重新核验原生会话与 Orca 位置的绑定", ErrNativePending)
		}
		if *at.Title != *prior.Title || at.Agent != prior.Agent || at.Kind != prior.Kind ||
			(at.Kind == "terminal" && (at.Handle != prior.Handle || at.PaneKey != prior.PaneKey)) {
			return fmt.Errorf("%w: 原生核验期间 Orca 位置已改变", ErrSkipped)
		}
		reader := checks.read
		if agent == "antigravity" && at.Kind == "chat" && checks.readChat != nil {
			reader = checks.readChat
		}
		observed, err := reader(ctx, agent, session, handle)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNativePending, err)
		}
		if observed != title {
			return ErrNativeStale
		}
		if at.Kind == "terminal" {
			live, err := terminalLayout(ctx, run, strings.TrimPrefix(at.ID, idPrefix), at)
			if err != nil || *live.Title != *at.Title || live.Handle != at.Handle || live.PaneTitle != at.PaneTitle {
				return fmt.Errorf("%w: terminal title or layout changed", ErrSkipped)
			}
		}
		if agent == "antigravity" && at.Kind == "chat" && *at.Title != title {
			origin := resolvePlaceOrigin(ctx, at, checks.origin)
			if origin != titleAuto {
				return fmt.Errorf("%w: Orca chat title is protected", ErrSkipped)
			}
		}
		return nil
	})
}

func syncTitle(ctx context.Context, run runCLI, id, expected, title string, verify preflight) error {
	rawID := strings.TrimPrefix(id, idPrefix)
	if !sessionID.MatchString(rawID) {
		return fmt.Errorf("%w：缺少有效 Orca 会话 ID", ErrSkipped)
	}
	if !validTitle(title) {
		return fmt.Errorf("%w：新标题无效", ErrSkipped)
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Second)
	defer cancel()
	before, err := observeForAgent(ctx, run, rawID, "")
	if err != nil {
		return err
	}
	if *before.Title != title && *before.Title != expected {
		return fmt.Errorf("%w：当前标题已改变或未经授权", ErrSkipped)
	}
	if err := verify(ctx, before); err != nil {
		return err
	}
	if *before.Title == title {
		return nil
	}
	writeArgs := []string{"chat", "rename", "--session", before.ID, "--name", title}
	if before.Kind == "terminal" {
		writeArgs = []string{"terminal", "rename", "--terminal", before.Handle, "--title", title}
	}
	writeErr := call(ctx, run, nil, writeArgs...)
	after, readErr := observeForAgent(ctx, run, rawID, before.Agent)
	if readErr == nil && *after.Title == title {
		return nil
	}
	cause := errors.Join(writeErr, readErr)
	if cause != nil {
		return fmt.Errorf("%w：%v", ErrUnconfirmed, cause)
	}
	return fmt.Errorf("%w：回读标题与目标不一致", ErrUnconfirmed)
}

func validTitle(title string) bool {
	return utf8.ValidString(title) && strings.TrimSpace(title) != "" &&
		!strings.HasPrefix(title, "--") && strings.IndexFunc(title, unicode.IsControl) < 0
}

func observe(ctx context.Context, run runCLI, id string) (place, error) {
	return observeForAgent(ctx, run, id, "")
}

func observeForAgent(ctx context.Context, run runCLI, id, agent string) (place, error) {
	if agent == "" || supportsAgent(agent) {
		if terminal, err := observeTerminal(ctx, run, id, agent); err == nil {
			return terminal, nil
		}
	}
	var list struct {
		Agents    []place `json:"agents"`
		Truncated *bool   `json:"truncated"`
	}
	if err := call(ctx, run, &list, "agent", "list"); err != nil {
		return place{}, errors.Join(ErrUnavailable, err)
	}
	var matches []place
	for _, agent := range list.Agents {
		if strings.TrimPrefix(agent.ID, idPrefix) == id {
			matches = append(matches, agent)
		}
	}
	if list.Truncated == nil || *list.Truncated || len(matches) != 1 || !localChat(matches[0]) {
		return place{}, fmt.Errorf("%w：列表未能唯一确认本机聊天", ErrSkipped)
	}
	return verifyPlace(ctx, run, id, matches[0])
}

type terminalEntry struct {
	Handle   string `json:"handle"`
	Orphaned bool   `json:"orphaned"`
	HostID   string `json:"executionHostId"`
	TabID    string `json:"tabId"`
	LeafID   string `json:"leafId"`
	Identity string `json:"agentIdentity"`
	Title    string `json:"title"`
}
type visualTab struct {
	TabID string          `json:"tabId"`
	Title string          `json:"title"`
	Panes json.RawMessage `json:"panes"`
}
type visualNode struct {
	Type     string          `json:"type"`
	Tabs     []visualTab     `json:"tabs"`
	Children []visualNode    `json:"children"`
	Splits   []visualNode    `json:"splits"`
	Panes    json.RawMessage `json:"panes"`
	Root     *visualNode     `json:"root"`
}

func observeTerminal(ctx context.Context, run runCLI, id, agent string) (place, error) {
	bound, err := showTerminal(ctx, run, id, agent)
	if err != nil {
		return place{}, err
	}
	return terminalLayout(ctx, run, id, bound)
}

func terminalLayout(ctx context.Context, run runCLI, id string, bound place) (place, error) {
	if bound.PaneKey == "" {
		return place{}, fmt.Errorf("%w: terminal binding", ErrSkipped)
	}
	var result struct {
		Truncated bool            `json:"truncated"`
		Terminals []terminalEntry `json:"terminals"`
		Layouts   json.RawMessage `json:"visualLayouts"`
	}
	if err := call(ctx, run, &result, "terminal", "list", "--include-visual-layouts"); err != nil || result.Truncated {
		return place{}, fmt.Errorf("%w: terminal list", ErrSkipped)
	}
	var match []terminalEntry
	for _, t := range result.Terminals {
		if !t.Orphaned && t.HostID == "local" && t.TabID+":"+t.LeafID == bound.PaneKey && (t.Identity == "" || t.Identity == bound.Agent) {
			match = append(match, t)
		}
	}
	if len(match) != 1 {
		return place{}, fmt.Errorf("%w: terminal not unique", ErrSkipped)
	}
	t := match[0]
	tabTitle, paneTitle, ok := findVisualTab(result.Layouts, t.TabID, t.LeafID, t.Handle)
	if !ok {
		return place{}, fmt.Errorf("%w: terminal layout", ErrSkipped)
	}
	return place{ID: id, Agent: bound.Agent, Kind: "terminal", HostID: "local", Title: &tabTitle, PaneKey: bound.PaneKey, Handle: t.Handle, PaneTitle: paneTitle}, nil
}

func findVisualTab(raw json.RawMessage, tabID, leafID, handle string) (string, string, bool) {
	var roots []visualNode
	if json.Unmarshal(raw, &roots) != nil {
		var one visualNode
		if json.Unmarshal(raw, &one) != nil {
			return "", "", false
		}
		roots = []visualNode{one}
	}
	var walk func(visualNode) (string, string, bool)
	walk = func(n visualNode) (string, string, bool) {
		for _, tab := range n.Tabs {
			var pane struct {
				Type   string `json:"type"`
				Handle string `json:"handle"`
				LeafID string `json:"leafId"`
				Title  string `json:"title"`
			}
			if json.Unmarshal(tab.Panes, &pane) == nil && pane.Type == "terminal" && pane.Handle == handle && pane.LeafID == leafID && tab.TabID == tabID {
				return tab.Title, pane.Title, true
			}
		}
		for _, c := range append(append(n.Children, n.Splits...), []visualNode{}...) {
			if t, p, ok := walk(c); ok {
				return t, p, ok
			}
		}
		if n.Root != nil {
			if t, p, ok := walk(*n.Root); ok {
				return t, p, ok
			}
		}
		return "", "", false
	}
	for _, n := range roots {
		if t, p, ok := walk(n); ok {
			return t, p, ok
		}
	}
	return "", "", false
}

func verifyPlace(ctx context.Context, run runCLI, id string, listed place) (place, error) {
	at, err := showChat(ctx, run, id)
	if err != nil {
		return place{}, err
	}
	if !localChat(at) || *at.Title != *listed.Title || at.Agent != listed.Agent {
		return place{}, fmt.Errorf("%w：Orca 列表和详情中的会话或标题不一致", ErrSkipped)
	}
	listed.ID = id
	return listed, nil
}

func showChat(ctx context.Context, run runCLI, id string) (place, error) {
	at, err := showAgentPlace(ctx, run, id)
	if err != nil {
		return place{}, err
	}
	if at.Kind != "chat" {
		return place{}, fmt.Errorf("%w: Orca chat location", ErrSkipped)
	}
	return at, nil
}

func showAgentPlace(ctx context.Context, run runCLI, id string) (place, error) {
	var shown struct {
		ID     string  `json:"orcaSessionId"`
		Kind   string  `json:"kind"`
		Places []place `json:"places"`
	}
	if err := call(ctx, run, &shown, "agent", "show", idPrefix+id); err != nil {
		return place{}, errors.Join(ErrUnavailable, err)
	}
	if strings.TrimPrefix(shown.ID, idPrefix) != id || len(shown.Places) != 1 {
		return place{}, fmt.Errorf("%w：Orca 会话位置不明确", ErrSkipped)
	}
	at := shown.Places[0]
	if (at.Kind != "chat" && at.Kind != "terminal") || at.Kind != shown.Kind || at.HostID != "local" || strings.TrimPrefix(at.ID, idPrefix) != id {
		return place{}, fmt.Errorf("%w：详情无法确认本机聊天", ErrSkipped)
	}
	return at, nil
}

func showTerminal(ctx context.Context, run runCLI, id, agent string) (place, error) {
	at, err := showAgentPlace(ctx, run, id)
	if err != nil || at.Kind != "terminal" || (agent != "" && at.Agent != agent) || !supportsAgent(at.Agent) || at.PaneKey == "" {
		return place{}, fmt.Errorf("%w: terminal binding", ErrSkipped)
	}
	return at, nil
}

func localChat(p place) bool {
	return p.Kind == "chat" && p.HostID == "local" && p.Title != nil
}

func call(ctx context.Context, run runCLI, target any, args ...string) error {
	out, err := run(ctx, append(args, "--environment", "local", "--json")...)
	if err != nil {
		return fmt.Errorf("Orca CLI %s %s：%w", args[0], args[1], err)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil || !envelope.OK {
		return fmt.Errorf("Orca CLI %s %s 返回无效响应或拒绝操作", args[0], args[1])
	}
	if target != nil && json.Unmarshal(envelope.Result, target) != nil {
		return fmt.Errorf("Orca CLI %s %s 返回无效结果", args[0], args[1])
	}
	return nil
}

func localCLI(ctx context.Context, args ...string) ([]byte, error) {
	cli, err := executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, cli, args...)
	for _, entry := range cmd.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "ORCA_PAIRING_CODE", "ORCA_ENVIRONMENT", "ORCA_REMOTE_PAIRING":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	hideWindow(cmd)
	return cmd.Output()
}

func executable() (string, error) {
	if override := os.Getenv("ORCA_CLI_COMMAND"); override != "" {
		return exec.LookPath(override)
	}
	if path, err := exec.LookPath("orca.exe"); err == nil {
		return path, nil
	}
	for _, base := range []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"), os.Getenv("ProgramFiles")} {
		if !filepath.IsAbs(base) {
			continue
		}
		path := filepath.Join(base, "orca", "resources", "bin", "orca.exe")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", errors.New("未安装或无法定位 orca.exe")
}
