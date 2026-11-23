package engine

import (
	"context"
	"database/sql"
	"errors"
)

type sessionRequest struct {
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
	Revision  string `json:"revision"`
}

type titleEligibility struct {
	Locked bool  `json:"locked"`
	Epoch  int64 `json:"epoch"`
	Seen   bool  `json:"seen"`
	title  string
}

func (e *Engine) titleEligibility(ctx context.Context, q queryer, p sessionRequest) (titleEligibility, error) {
	if !validKey(p.Agent, p.SessionID) || !validString(p.Revision, 512) {
		return titleEligibility{}, errors.New("会话 ID 无效")
	}
	var state titleEligibility
	err := q.QueryRowContext(ctx, `SELECT protection_reason='manual_locked',protection_epoch,title,
		EXISTS(SELECT 1 FROM events WHERE agent=s.agent AND session_id=s.session_id AND revision=? AND native_only=0)
		FROM sessions s WHERE agent=? AND session_id=?`, p.Revision, p.Agent, p.SessionID).
		Scan(&state.Locked, &state.Epoch, &state.title, &state.Seen)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return state, err
}

type ProtectionTarget struct {
	Agent, SessionID, OrcaID, NativeHandle, Revision string
	Epoch                                            int64
}

func (e *Engine) protectionTarget(ctx context.Context, p idRequest) (ProtectionTarget, error) {
	var target ProtectionTarget
	err := e.db.QueryRowContext(ctx, `
		SELECT s.agent,s.session_id,s.orca_id,s.revision,s.protection_epoch,
			coalesce((SELECT native_handle FROM events
				WHERE agent=s.agent AND session_id=s.session_id AND orca_id=s.orca_id AND native_handle<>''
				ORDER BY rowid DESC LIMIT 1),'')
		FROM sessions s JOIN events e USING(agent,session_id) WHERE e.id=? AND s.manual=1`, p.ID).
		Scan(&target.Agent, &target.SessionID, &target.OrcaID, &target.Revision, &target.Epoch, &target.NativeHandle)
	if errors.Is(err, sql.ErrNoRows) {
		err = errors.New("会话保护状态已更新，请刷新记录")
	}
	return target, err
}

type protectionRelease struct {
	ProtectionTarget
	NativeTitle string
	OrcaTitle   *string
}

func (e *Engine) releaseProtection(ctx context.Context, p protectionRelease) (accepted, error) {
	if !validKey(p.Agent, p.SessionID) || !validString(p.NativeTitle, 2048) ||
		(p.OrcaID != "" && p.OrcaTitle == nil) ||
		(p.OrcaTitle != nil && !validString(*p.OrcaTitle, 2048)) {
		return accepted{}, errors.New("现有标题未完整确认，无法解除保护")
	}
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	result, err := transact(ctx, e.db, func(tx *sql.Tx) (accepted, error) {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET title=?,orca_unlocked_base=?,manual=0,
			protection_reason='',protection_epoch=protection_epoch+1 WHERE agent=? AND session_id=?
			AND orca_id=? AND revision=? AND protection_epoch=? AND manual=1`,
			p.NativeTitle, p.OrcaTitle, p.Agent, p.SessionID, p.OrcaID, p.Revision, p.Epoch)
		if err != nil {
			return accepted{}, err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return accepted{}, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE events SET
			status=CASE WHEN status IN ('queued','running','candidate','claimed') THEN 'stale' ELSE status END,
			sync_status=CASE WHEN sync_status IN ('pending','writing','failed','protected','blocked') THEN 'stale' ELSE sync_status END,
			payload=NULL,updated=?
			WHERE agent=? AND session_id=? AND (status IN ('queued','running','candidate','claimed')
			    OR sync_status IN ('pending','writing','failed','protected','blocked'))`,
			e.now().UnixMilli(), p.Agent, p.SessionID)
		return accepted{Accepted: err == nil}, err
	})
	if err == nil && result.Accepted {
		e.cancelSuperseded(p.Agent, p.SessionID, false)
	}
	return result, err
}
