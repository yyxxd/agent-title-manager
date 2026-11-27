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
	if !ok || s.backend.ReadProtectionTitles == nil {
		return nil, errors.New("无法确认会话及原生标题")
	}
	native, orcaTitle, err := s.backend.ReadProtectionTitles(ctx, target.Agent, target.SessionID, target.NativeHandle, target.OrcaID)
	if err != nil {
		return nil, err
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
