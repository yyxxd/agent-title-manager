package engine

import (
	"context"
	"errors"
)

type deferredTitle struct {
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
}

func (e *Engine) deferTitle(ctx context.Context, p deferredTitle) (accepted, error) {
	if p.Agent != "codex" || !validKey(p.Agent, p.SessionID) || p.ID == "" || !validString(p.ID, 128) {
		return accepted{}, errors.New("待处理标题标识无效")
	}
	res, err := e.db.ExecContext(ctx, `
		UPDATE events SET status='needs_review',decision_reason='baseline_unverified',updated=?
		WHERE agent=? AND session_id=? AND id=? AND status='claimed'`, e.now().UnixMilli(), p.Agent, p.SessionID, p.ID)
	if err != nil {
		return accepted{}, err
	}
	n, err := res.RowsAffected()
	if err == nil && n > 0 {
		signal(e.modelWake)
	}
	return accepted{Accepted: n > 0}, err
}
