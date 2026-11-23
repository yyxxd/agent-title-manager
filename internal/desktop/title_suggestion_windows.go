package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

func (s *Service) suggestTitle(ctx context.Context, body json.RawMessage) (engine.TitleSuggestion, error) {
	var request struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &request) != nil || s.backend.SuggestTitle == nil || s.backend.ReadNamingSnapshot == nil {
		return engine.TitleSuggestion{}, errors.New("无法生成标题建议")
	}
	return s.backend.SuggestTitle(ctx, request.ID, s.readSuggestionObservation)
}

func (s *Service) readSuggestionObservation(ctx context.Context, target engine.SuggestionTarget) (engine.SuggestionObservation, error) {
	var observation engine.SuggestionObservation
	if s.backend.Enabled == nil {
		return observation, errors.New("无法确认 Agent 插件状态")
	}
	if enabled, err := s.backend.Enabled(ctx, target.Agent); err != nil || !enabled {
		return observation, errors.New("请先启用该 Agent 的标题插件")
	}
	if target.OrcaID != "" {
		if s.backend.ResolveTitleOrigin == nil {
			return observation, errors.New("无法确认关联的 Orca 会话")
		}
		bindingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		binding, ok := s.backend.ResolveTitleOrigin(bindingCtx, target.Agent, target.SessionID, target.NativeHandle, target.OrcaID)
		cancel()
		if !ok || binding.ID != target.OrcaID {
			return observation, errors.New("无法确认关联的 Orca 会话，请打开对应会话后重试")
		}
		scope, err := orcasync.NativeNamingScope(ctx, target.Agent, target.SessionID, binding)
		if err != nil {
			return observation, errors.New("无法确认 Orca 使用的原生账户")
		}
		observation.Scope, observation.OrcaTitle, observation.OrcaOrigin = scope, binding.Title, binding.Origin
	}
	snapshot, err := s.backend.ReadNamingSnapshot(ctx, target.Agent, target.SessionID, target.NativeHandle, observation.Scope)
	if err != nil {
		return observation, errors.New("原生会话或内容无法读取，请确认会话仍存在且当前账户可访问")
	}
	observation.NamingSnapshot = snapshot
	return observation, nil
}
