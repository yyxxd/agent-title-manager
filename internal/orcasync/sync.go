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
	ID     string  `json:"orcaSessionId"`
	Agent  string  `json:"agent"`
	Kind   string  `json:"kind"`
	HostID string  `json:"hostId"`
	Title  *string `json:"title"`
}

// SyncVerified requires a fresh read of the bound native session immediately
// before asking Orca to rename it. Orca may also update the native title.
func SyncVerified(ctx context.Context, id, expected, title, agent, session, handle string, reader NativeTitleReader) error {
	lock.Lock()
	defer lock.Unlock()
	return syncVerified(ctx, localCLI, id, expected, title, agent, session, handle, reader)
}

func syncVerified(ctx context.Context, run runCLI, id, expected, title, agent, session, handle string, reader NativeTitleReader) error {
	if reader == nil {
		return ErrNativePending
	}
	return syncTitle(ctx, run, id, expected, title, func(ctx context.Context, prior place) error {
		at, bound := boundChat(ctx, run, agent, session, handle, prior.ID)
		if !bound || at.Title == nil {
			return fmt.Errorf("%w：无法重新核验原生会话与 Orca 聊天的绑定", ErrNativePending)
		}
		if *at.Title != *prior.Title || at.Agent != prior.Agent {
			return fmt.Errorf("%w：原生核验期间 Orca 聊天已改变", ErrSkipped)
		}
		observed, err := reader(ctx, agent, session, handle)
		if err != nil {
			return fmt.Errorf("%w：原生宿主标题尚不可读", ErrNativePending)
		}
		if observed != title {
			return ErrNativeStale
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
	before, err := observe(ctx, run, rawID)
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
	writeErr := call(ctx, run, nil, "chat", "rename", "--session", before.ID, "--name", title)
	after, readErr := observe(ctx, run, rawID)
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
	var shown struct {
		ID     string  `json:"orcaSessionId"`
		Kind   string  `json:"kind"`
		Places []place `json:"places"`
	}
	if err := call(ctx, run, &shown, "agent", "show", idPrefix+id); err != nil {
		return place{}, errors.Join(ErrUnavailable, err)
	}
	if strings.TrimPrefix(shown.ID, idPrefix) != id || shown.Kind != "chat" || len(shown.Places) != 1 {
		return place{}, fmt.Errorf("%w：Orca 会话位置不明确", ErrSkipped)
	}
	at := shown.Places[0]
	if at.Kind != "chat" || at.HostID != "local" || strings.TrimPrefix(at.ID, idPrefix) != id {
		return place{}, fmt.Errorf("%w：详情无法确认本机聊天", ErrSkipped)
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
