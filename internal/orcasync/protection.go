package orcasync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (i *Integration) ReadProtectionTitles(ctx context.Context, agent, session, handle, id string) (string, *string, error) {
	if id != "" {
		return i.readBoundProtection(ctx, agent, session, handle, id)
	}
	if i.ReadNative == nil {
		return "", nil, errProtectionNative
	}
	native, err := i.ReadNative(ctx, agent, session, handle)
	if err != nil {
		return "", nil, errProtectionNative
	}
	return native, nil, nil
}

var errProtectionNative = errors.New("无法读取原生标题，请打开对应会话后重试")
var errProtectionBinding = errors.New("无法读取对应的 Orca 标题，请确认 Orca 和会话可用后重试")

func (i *Integration) readBoundProtection(ctx context.Context, agent, session, handle, id string) (string, *string, error) {
	if agent == "antigravity" && handle != session {
		return "", nil, errProtectionBinding
	}
	binding, verified := i.resolve(ctx, agent, session, handle, id, "", []time.Duration{0})
	if !verified || binding.ID != id {
		return "", nil, errProtectionBinding
	}
	account, err := NativeNamingScope(ctx, agent, session, binding)
	if err != nil {
		return "", nil, errProtectionNative
	}
	var native string
	if account != "" && i.ReadNativeAt != nil {
		native, err = i.ReadNativeAt(ctx, agent, session, account)
	} else if account == "" && i.ReadNative != nil {
		native, err = i.ReadNative(ctx, agent, session, binding.Handle)
	} else {
		return "", nil, errProtectionNative
	}
	if err != nil {
		return "", nil, errProtectionNative
	}
	// 原生读取期间，Orca 的会话绑定、账户或标题可能已经变化。
	refreshed, verified := i.resolve(ctx, agent, session, handle, id, binding.Surface, []time.Duration{0})
	if !verified || refreshed != binding {
		return "", nil, errProtectionBinding
	}
	current, err := NativeNamingScope(ctx, agent, session, refreshed)
	if err != nil || !samePath(current, account) {
		return "", nil, errProtectionBinding
	}
	return native, &refreshed.Title, nil
}

func (i *Integration) readAntigravityChatTitle(ctx context.Context, id, session string) (string, error) {
	root, err := acpRoot(ctx, id, session)
	if err != nil || i.ReadNativeAt == nil {
		return "", fmt.Errorf("%w: 无法确认 Antigravity Chat 数据目录", ErrNativePending)
	}
	if !i.Active(ctx, "antigravity") {
		return "", ErrSkipped
	}
	return i.ReadNativeAt(ctx, "antigravity", session, root)
}
