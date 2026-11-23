package orcasync

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Gate func(context.Context, string) (bool, error)

type Integration struct {
	Enabled    Gate
	Changes    func() <-chan struct{}
	Validate   NativeHandleValidator
	ReadNative NativeTitleReader
	ReadScoped func(context.Context, string, string) (string, error)

	run    runCLI
	origin func(context.Context, string, string, string) string
}

func (i *Integration) Active(ctx context.Context, agent string) bool {
	if ctx.Err() != nil || i.Enabled == nil {
		return false
	}
	enabled, err := i.Enabled(ctx, agent)
	return ctx.Err() == nil && err == nil && enabled
}

func (i *Integration) NativeTitleMatches(ctx context.Context, agent, session, handle, title string) (bool, error) {
	if !i.Active(ctx, agent) || i.ReadNative == nil {
		return false, nil
	}
	ctx, cancel := i.operation(ctx, agent)
	defer cancel()
	current, err := i.ReadNative(ctx, agent, session, handle)
	return current == title && i.Active(ctx, agent), err
}

// 每次外部操作前重新读取开关，配置变动也会取消已启动的命令。
func (i *Integration) operation(parent context.Context, agent string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if i.Changes != nil {
		changed := i.Changes()
		go func() {
			for {
				if !i.Active(ctx, agent) {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-changed:
					changed = i.Changes()
				}
			}
		}()
	}
	return ctx, cancel
}

func (i *Integration) command(agent string) runCLI {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if !i.Active(ctx, agent) {
			return nil, ErrSkipped
		}
		if i.run != nil {
			return i.run(ctx, args...)
		}
		return localCLI(ctx, args...)
	}
}

func databaseOrigin(ctx context.Context, agent, id, title string) string {
	return titleOrigin(ctx, orcaDatabase(), agent, id, title)
}

func (i *Integration) titleOrigin(ctx context.Context, agent, id, title string) string {
	if !i.Active(ctx, agent) {
		return titleUnknown
	}
	if i.origin != nil {
		return i.origin(ctx, agent, id, title)
	}
	return databaseOrigin(ctx, agent, id, title)
}

func (i *Integration) ResolveTitleOrigin(ctx context.Context, agent, session, handle, hint string) (Binding, bool) {
	kind := ""
	if agent == "antigravity" {
		if handle == "" {
			return Binding{}, false
		}
		kind = "terminal"
	}
	return i.resolve(ctx, agent, session, handle, hint, kind, []time.Duration{0})
}

func (i *Integration) RetryBinding(ctx context.Context, agent, session, handle, hint string) (Binding, bool) {
	kind := ""
	if agent == "antigravity" {
		if handle == "" {
			return Binding{}, false
		}
		kind = "terminal"
	}
	return i.resolve(ctx, agent, session, handle, hint, kind, []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond, 1500 * time.Millisecond})
}

func (i *Integration) resolve(ctx context.Context, agent, session, handle, hint, kind string, delays []time.Duration) (Binding, bool) {
	if !i.Active(ctx, agent) {
		return Binding{}, false
	}
	if handle == "" && agent != "pi" {
		handle = session
	}
	ctx, cancel := i.operation(ctx, agent)
	defer cancel()
	at, ok := discover(ctx, i.command(agent), i.Validate, agent, session, handle, hint, kind, delays)
	if !ok {
		return Binding{}, false
	}
	origin := resolvePlaceOrigin(ctx, at, i.titleOrigin)
	if !i.Active(ctx, agent) {
		return Binding{}, false
	}
	id := strings.TrimPrefix(at.ID, idPrefix)
	category := ""
	if at.Kind == "chat" && agent == "codex" {
		category = codexSessionKind(ctx, id)
	}
	return Binding{ID: id, Title: *at.Title, Origin: origin, Handle: handle, Kind: category}, true
}

func (i *Integration) SyncVerified(ctx context.Context, id, expected, title, agent, session, handle string) error {
	if !i.Active(ctx, agent) {
		return ErrSkipped
	}
	ctx, cancel := i.operation(ctx, agent)
	defer cancel()
	lock.Lock()
	defer lock.Unlock()
	return syncWithChecks(ctx, i.command(agent), id, expected, title, agent, session, handle, syncChecks{
		validate: i.Validate, read: i.ReadNative, origin: i.titleOrigin,
		readChat: func(ctx context.Context, _, session, _ string) (string, error) {
			if !i.Active(ctx, agent) {
				return "", ErrSkipped
			}
			root, err := acpRoot(ctx, id, session)
			if err != nil || i.ReadScoped == nil {
				return "", fmt.Errorf("%w: 无法确认 Antigravity Chat 数据目录", ErrNativePending)
			}
			if !i.Active(ctx, agent) {
				return "", ErrSkipped
			}
			return i.ReadScoped(ctx, session, root)
		},
	})
}
