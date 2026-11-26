package orcasync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (i *Integration) ReadProtectionTitles(ctx context.Context, agent, session, handle, id string) (string, *string, error) {
	if agent == "antigravity" && id != "" {
		return i.readBoundAntigravityProtection(ctx, session, handle, id)
	}
	if i.ReadNative == nil {
		return "", nil, errProtectionNative
	}
	native, err := i.ReadNative(ctx, agent, session, handle)
	if err != nil {
		return "", nil, errProtectionNative
	}
	if id == "" {
		return native, nil, nil
	}
	binding, verified := i.RetryBinding(ctx, agent, session, handle, id)
	if !verified || binding.ID != id {
		return "", nil, errProtectionBinding
	}
	return native, &binding.Title, nil
}

var errProtectionNative = errors.New("无法读取原生标题，请打开对应会话后重试")
var errProtectionBinding = errors.New("无法读取对应的 Orca 标题，请确认 Orca 和会话可用后重试")

func (i *Integration) readBoundAntigravityProtection(ctx context.Context, session, handle, id string) (string, *string, error) {
	const agent = "antigravity"
	if handle != session {
		return "", nil, errProtectionBinding
	}
	binding, verified := i.resolve(ctx, agent, session, handle, id, "", []time.Duration{0})
	if !verified || binding.ID != id {
		return "", nil, errProtectionBinding
	}
	var native string
	var err error
	if binding.Surface == "chat" {
		native, err = i.readAntigravityChatTitle(ctx, id, session)
	} else if i.ReadNative != nil {
		native, err = i.ReadNative(ctx, agent, session, handle)
	} else {
		return "", nil, errProtectionNative
	}
	if err != nil {
		return "", nil, errProtectionNative
	}
	// 原生读取期间，Orca 的会话绑定或标题可能已经变化。
	refreshed, verified := i.resolve(ctx, agent, session, handle, id, binding.Surface, []time.Duration{0})
	if !verified || refreshed != binding {
		return "", nil, errProtectionBinding
	}
	return native, &refreshed.Title, nil
}

func (i *Integration) readAntigravityChatTitle(ctx context.Context, id, session string) (string, error) {
	root, err := acpRoot(ctx, id, session)
	if err != nil || i.ReadScoped == nil {
		return "", fmt.Errorf("%w: 无法确认 Antigravity Chat 数据目录", ErrNativePending)
	}
	if !i.Active(ctx, "antigravity") {
		return "", ErrSkipped
	}
	return i.ReadScoped(ctx, session, root)
}
