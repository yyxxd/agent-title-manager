package engine

import (
	"context"
	"database/sql"
	"errors"
)

type claimRequest struct {
	Agent        string `json:"agent"`
	SessionID    string `json:"sessionId"`
	CurrentTitle string `json:"currentTitle"`
	Revision     string `json:"revision"`
}
type candidate struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	BaseTitle string `json:"baseTitle"`
	Revision  string `json:"revision"`
}
type claimResponse struct {
	Candidate *candidate `json:"candidate"`
}
type ackRequest struct {
	Agent         string `json:"agent"`
	SessionID     string `json:"sessionId"`
	ID            string `json:"id"`
	ObservedTitle string `json:"observedTitle"`
}

func (e *Engine) claim(ctx context.Context, p claimRequest) (claimResponse, error) {
	if !validKey(p.Agent, p.SessionID) || !validString(p.CurrentTitle, 2048) || !validString(p.Revision, 512) {
		return claimResponse{}, errors.New("会话键、版本或标题无效")
	}
	return transact(ctx, e.db, func(tx *sql.Tx) (claimResponse, error) {
		s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return claimResponse{}, nil
		}
		if err != nil || s.manual || (p.Revision != "" && p.Revision != s.revision) {
			return claimResponse{}, err
		}
		if s.title != p.CurrentTitle {
			return claimResponse{}, e.observeChangedTitle(ctx, tx, p)
		}
		var c candidate
		err = tx.QueryRowContext(ctx, `SELECT id,title,base,revision FROM events WHERE agent=? AND session_id=?
			AND revision=? AND base=? AND status IN ('candidate','claimed')`,
			p.Agent, p.SessionID, s.revision, p.CurrentTitle).Scan(&c.ID, &c.Title, &c.BaseTitle, &c.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return claimResponse{}, nil
		}
		if err != nil {
			return claimResponse{}, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE events SET status='claimed',updated=? WHERE id=? AND status='candidate'`, e.now().UnixMilli(), c.ID)
		return claimResponse{&c}, err
	})
}

func (e *Engine) observeChangedTitle(ctx context.Context, tx *sql.Tx, p claimRequest) error {
	var inFlight bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE agent=? AND session_id=?
		AND status='claimed' AND title=?)`, p.Agent, p.SessionID, p.CurrentTitle).Scan(&inFlight)
	if err != nil || inFlight {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET manual=1,title=? WHERE agent=? AND session_id=?`,
		p.CurrentTitle, p.Agent, p.SessionID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE events SET status='stale',payload=NULL,updated=?
		WHERE agent=? AND session_id=? AND status IN ('queued','candidate','claimed')`, e.now().UnixMilli(), p.Agent, p.SessionID)
	return err
}

func (e *Engine) ack(ctx context.Context, p ackRequest) (accepted, error) {
	if !validKey(p.Agent, p.SessionID) || p.ID == "" || !validString(p.ID, 128) || !validString(p.ObservedTitle, 2048) {
		return accepted{}, errors.New("回执无效")
	}
	result, err := transact(ctx, e.db, func(tx *sql.Tx) (accepted, error) {
		var status, base, title, revision, orcaID, origin string
		var orcaBase *string
		err := tx.QueryRowContext(ctx, `SELECT status,base,title,revision,orca_id,orca_base,orca_origin
			FROM events WHERE id=? AND agent=? AND session_id=?`, p.ID, p.Agent, p.SessionID).
			Scan(&status, &base, &title, &revision, &orcaID, &orcaBase, &origin)
		if errors.Is(err, sql.ErrNoRows) {
			return accepted{}, nil
		}
		if err != nil {
			return accepted{}, err
		}
		if p.ObservedTitle != title {
			return accepted{}, nil
		}
		if status == "applied" {
			return accepted{true}, nil
		}
		if status != "claimed" {
			return accepted{}, nil
		}
		s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
		if err != nil {
			return accepted{}, err
		}
		if s.manual || (s.title != title && (s.revision != revision || s.title != base)) {
			return accepted{}, nil
		}
		return accepted{true}, e.applyAck(ctx, tx, p, title, syncPolicy(orcaID, orcaBase, origin, s.orcaManaged, title))
	})
	if err == nil && result.Accepted {
		signal(e.syncWake)
	}
	return result, err
}

func (e *Engine) applyAck(ctx context.Context, tx *sql.Tx, p ackRequest, title, syncStatus string) error {
	now := e.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE events SET status='applied',sync_status=?,updated=?,finalized=? WHERE id=?`,
		syncStatus, now, now, p.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET title=? WHERE agent=? AND session_id=?`,
		title, p.Agent, p.SessionID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE events SET status='stale',payload=NULL,updated=?
		WHERE agent=? AND session_id=? AND id<>? AND base<>? AND status IN ('queued','candidate','claimed')`,
		now, p.Agent, p.SessionID, p.ID, title)
	return err
}
