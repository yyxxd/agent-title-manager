package desktop

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func (s *Service) unlockRecord(ctx context.Context, params json.RawMessage) (any, error) {
	value, err := s.backend.Engine(ctx, "records.protectionTarget", params)
	if err != nil {
		return nil, err
	}
	target, ok := value.(engine.ProtectionTarget)
	if !ok || s.backend.ReadNativeTitle == nil {
		return nil, errors.New("无法确认会话及原生标题")
	}
	native, err := s.backend.ReadNativeTitle(ctx, target.Agent, target.SessionID, target.NativeHandle)
	if err != nil {
		return nil, errors.New("无法读取原生标题，请打开对应会话后重试")
	}
	var orcaTitle *string
	if target.OrcaID != "" {
		if s.backend.RetryBinding == nil {
			return nil, errors.New("无法确认 Orca 会话")
		}
		binding, verified := s.backend.RetryBinding(ctx, target.Agent, target.SessionID, target.NativeHandle, target.OrcaID)
		if !verified || binding.ID != target.OrcaID {
			return nil, errors.New("无法读取对应的 Orca 标题，请确认 Orca 和会话可用后重试")
		}
		orcaTitle = &binding.Title
	}
	request, err := json.Marshal(struct {
		engine.ProtectionTarget
		NativeTitle string
		OrcaTitle   *string
	}{target, native, orcaTitle})
	if err != nil {
		return nil, err
	}
	return s.backend.Engine(ctx, "records.releaseProtection", request)
}
