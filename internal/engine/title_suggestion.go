package engine

import (
	"context"
	"database/sql"
	"errors"
)

type SuggestionTarget struct {
	Agent, SessionID, NativeHandle, OrcaID string
	revision, title, protection            string
	epoch                                  int64
}

type NamingTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type NamingSnapshot struct {
	SessionID, Revision, CurrentTitle, TitleOrigin, Project, OriginalGoal string
	Turns                                                                 []NamingTurn
}

// SuggestionObservation contains verified native data and the linked host
// baseline. Only the bounded prompt is sent to the model.
type SuggestionObservation struct {
	NamingSnapshot
	Scope, OrcaTitle, OrcaOrigin string
}

type TitleSuggestion struct {
	Title   string        `json:"title,omitempty"`
	Failure failureReason `json:"failure"`
}

func (e *Engine) suggestionTarget(ctx context.Context, id string) (SuggestionTarget, error) {
	var target SuggestionTarget
	if id == "" || !validString(id, 128) {
		return target, errors.New("记录 ID 无效")
	}
	err := e.db.QueryRowContext(ctx, `SELECT s.agent,s.session_id,s.revision,s.title,s.orca_id,s.protection_reason,
		s.protection_epoch,coalesce(nullif((SELECT native_handle FROM events WHERE agent=s.agent AND session_id=s.session_id
		AND native_handle<>'' AND orca_id=s.orca_id ORDER BY rowid DESC LIMIT 1),''),s.session_id)
		FROM sessions s JOIN events record USING(agent,session_id) WHERE record.id=?`, id).
		Scan(&target.Agent, &target.SessionID, &target.revision, &target.title, &target.OrcaID,
			&target.protection, &target.epoch, &target.NativeHandle)
	if errors.Is(err, sql.ErrNoRows) {
		return target, errors.New("记录已不存在，请刷新后重试")
	}
	return target, err
}

func (e *Engine) SuggestTitle(ctx context.Context, id string, read func(context.Context, SuggestionTarget) (SuggestionObservation, error)) (TitleSuggestion, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()

	target, err := e.suggestionTarget(ctx, id)
	if err != nil {
		return TitleSuggestion{}, err
	}
	key := [2]string{target.Agent, target.SessionID}
	if _, duplicate := e.suggestions.LoadOrStore(key, struct{}{}); duplicate {
		return TitleSuggestion{}, errors.New("该会话正在生成建议，请等待结果")
	}
	defer e.suggestions.Delete(key)

	before, err := read(ctx, target)
	if err != nil {
		return TitleSuggestion{}, err
	}
	payload, err := suggestionPrompt(target, before.NamingSnapshot)
	if err != nil {
		return TitleSuggestion{}, err
	}
	if err := e.checkSuggestionTarget(ctx, id, target); err != nil {
		return TitleSuggestion{}, err
	}
	settings, secret, err := loadSettings(ctx, e.db)
	if err != nil {
		return TitleSuggestion{}, err
	}
	result, err := e.invoke(ctx, attemptRequest{agent: target.Agent, provider: settings.Provider,
		settings: settings, secret: secret, payload: payload, suggestion: true})
	if err != nil {
		return TitleSuggestion{}, err
	}
	title, state, _, failure := modelOutcome(before.CurrentTitle, true, result, settings.MaxTitleLength, 0)
	switch state {
	case "disabled":
		return TitleSuggestion{}, errors.New("请先启用该 Agent 的标题插件")
	case "kept":
		return TitleSuggestion{}, errors.New("模型没有给出不同的标题，可以重新生成建议")
	case "candidate":
	default:
		return TitleSuggestion{Failure: failure}, nil
	}
	after, err := read(ctx, target)
	if err != nil {
		return TitleSuggestion{}, err
	}
	if before.Revision != after.Revision || before.CurrentTitle != after.CurrentTitle || before.TitleOrigin != after.TitleOrigin ||
		before.SessionID != after.SessionID || before.Scope != after.Scope ||
		before.OrcaTitle != after.OrcaTitle || before.OrcaOrigin != after.OrcaOrigin {
		return TitleSuggestion{}, errSuggestionChanged
	}
	if err := e.checkSuggestionTarget(ctx, id, target); err != nil {
		return TitleSuggestion{}, err
	}
	return TitleSuggestion{Title: title}, nil
}

var errSuggestionChanged = errors.New("会话内容、标题或保护状态已变化，请刷新后重新生成")

func (e *Engine) checkSuggestionTarget(ctx context.Context, id string, target SuggestionTarget) error {
	current, err := e.suggestionTarget(ctx, id)
	if err != nil {
		return err
	}
	if current != target {
		return errSuggestionChanged
	}
	return nil
}

func suggestionPrompt(target SuggestionTarget, snapshot NamingSnapshot) ([]byte, error) {
	if snapshot.SessionID != target.SessionID || !validString(snapshot.CurrentTitle, 2048) || !validOrigin(snapshot.TitleOrigin) {
		return nil, errors.New("无法确认原生会话身份或标题")
	}
	turns := make([]turn, 0, len(snapshot.Turns))
	for _, message := range snapshot.Turns {
		if message.Role == "user" || message.Role == "assistant" {
			turns = append(turns, turn(message))
		}
	}
	if snapshot.Revision == "" {
		return nil, errors.New("没有可用的已完成对话，若会话正在运行，请结束后重试")
	}
	payload, err := makePrompt(submission{Turns: turns, OriginalGoal: snapshot.OriginalGoal,
		CurrentTitle: snapshot.CurrentTitle, Project: snapshot.Project, Suggestion: true})
	if err == nil && payload == nil {
		err = errors.New("没有可用于命名的会话文字")
	}
	return payload, err
}
