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
	Status    string     `json:"status,omitempty"`
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
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	manual := false
	result, err := transact(ctx, e.db, func(tx *sql.Tx) (claimResponse, error) {
		s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return claimResponse{Status: "none"}, nil
		}
		if err != nil {
			return claimResponse{}, err
		}
		revision := p.Revision
		if revision == "" {
			revision = s.revision
		}
		if revision != s.revision {
			return claimResponse{Status: "stale"}, nil
		}
		var c candidate
		var status string
		err = tx.QueryRowContext(ctx, `SELECT id,title,base,revision,status FROM events WHERE agent=? AND session_id=?
			AND revision=? AND native_only=0`, p.Agent, p.SessionID, revision).Scan(&c.ID, &c.Title, &c.BaseTitle, &c.Revision, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return claimResponse{}, err
		}
		if errors.Is(err, sql.ErrNoRows) {
			status = "none"
		}
		if s.title != p.CurrentTitle {
			status, err = e.observeChangedTitle(ctx, tx, p)
			manual = status == "stale"
			return claimResponse{Status: status}, err
		}
		if status != "candidate" && status != "claimed" && !s.manual && s.title == "" {
			initial, active, err := initialClaim(ctx, tx, p.Agent, p.SessionID)
			if err != nil {
				return claimResponse{}, err
			}
			if active != "" {
				c, status = initial, active
				c.Revision = revision
			}
		}
		if status != "candidate" && status != "claimed" {
			return claimResponse{Status: status}, nil
		}
		if s.manual || c.BaseTitle != p.CurrentTitle {
			return claimResponse{Status: "stale"}, nil
		}
		return e.issueClaim(ctx, tx, c, status)
	})
	if err == nil {
		if manual {
			e.cancelSuperseded(p.Agent, p.SessionID, false)
		}
		if result.Status == "kept" {
			signal(e.modelWake)
		}
	}
	return result, err
}

func initialClaim(ctx context.Context, tx *sql.Tx, agent, sessionID string) (candidate, string, error) {
	var c candidate
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,title,base,revision,status FROM events
		WHERE agent=? AND session_id=? AND native_only=0 AND base=''
		  AND status IN ('candidate','claimed','running')
		ORDER BY CASE WHEN status='running' THEN 1 ELSE 0 END, rowid LIMIT 1`, agent, sessionID).
		Scan(&c.ID, &c.Title, &c.BaseTitle, &c.Revision, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return candidate{}, "", nil
	}
	return c, status, err
}

func (e *Engine) issueClaim(ctx context.Context, tx *sql.Tx, c candidate, status string) (claimResponse, error) {
	if status == "claimed" {
		return claimResponse{Candidate: &c, Status: "claimed"}, nil
	}
	now := e.now().UnixMilli()
	conflict, err := titleConflict(ctx, tx, c.ID, c.Title)
	if err != nil {
		return claimResponse{}, err
	}
	if conflict {
		_, err = tx.ExecContext(ctx, `UPDATE events SET status='kept',title=base,updated=?,finalized=? WHERE id=?`, now, now, c.ID)
		return claimResponse{Status: "kept"}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE events SET status='claimed',updated=? WHERE id=?`, now, c.ID)
	return claimResponse{Candidate: &c, Status: "claimed"}, err
}

func (e *Engine) observeChangedTitle(ctx context.Context, tx *sql.Tx, p claimRequest) (string, error) {
	var inFlight bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE agent=? AND session_id=?
		AND status='claimed' AND title=?)`, p.Agent, p.SessionID, p.CurrentTitle).Scan(&inFlight)
	if err != nil {
		return "", err
	}
	if inFlight {
		return "claimed", nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET manual=1,title=? WHERE agent=? AND session_id=?`,
		p.CurrentTitle, p.Agent, p.SessionID); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE events SET status='stale',payload=NULL,updated=?
		WHERE agent=? AND session_id=? AND status IN ('queued','running','candidate','claimed')`, e.now().UnixMilli(), p.Agent, p.SessionID)
	return "stale", err
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
			var existing string
			if err := tx.QueryRowContext(ctx, `SELECT sync_status FROM events WHERE id=?`, p.ID).Scan(&existing); err != nil {
				return accepted{}, err
			}
			return accepted{Accepted: true, SyncStatus: existing}, nil
		}
		if status != "claimed" {
			return accepted{}, nil
		}
		s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
		if err != nil {
			return accepted{}, err
		}
		if s.manual || (s.title != title && (s.title != base || s.revision != revision && base != "")) {
			return accepted{}, nil
		}
		syncStatus := syncPolicy(orcaID, orcaBase, origin, s.orcaManaged, title)
		return accepted{Accepted: true, SyncStatus: syncStatus, EventRevision: revision, SessionRevision: s.revision},
			e.applyAck(ctx, tx, p, title, syncStatus)
	})
	if err == nil && result.Accepted {
		signal(e.syncWake)
		signal(e.modelWake)
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
