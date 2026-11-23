package engine

import (
	"context"
	"database/sql"
	"errors"
)

type ConfirmedBinding struct {
	ID, Agent, SessionID, Revision, NativeTitle string
	OrcaID, OrcaTitle, Origin, NativeHandle     string
	SessionRevision                             string
	Kind                                        string
}

func UnboundAckRevisions(result any) (event, session string, ok bool) {
	ack, ok := result.(accepted)
	if !ok || !ack.Accepted || ack.SyncStatus != "unbound" || ack.EventRevision == "" {
		return "", "", false
	}
	return ack.EventRevision, ack.SessionRevision, true
}

// AttachBinding 补充已确认的同一条记录，不重新提交原生事件或创建命名任务。
func (e *Engine) AttachBinding(ctx context.Context, p ConfirmedBinding) error {
	if !validKey(p.Agent, p.SessionID) || p.OrcaID == "" || !validString(p.OrcaID, 256) ||
		!validString(p.OrcaTitle, 2048) || !validString(p.NativeHandle, 4096) || !validOrigin(p.Origin) {
		return errors.New("Orca 绑定无效")
	}
	if p.SessionRevision == "" {
		p.SessionRevision = p.Revision
	}
	changed, err := transact(ctx, e.db, func(tx *sql.Tx) (bool, error) {
		s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (s.revision != p.SessionRevision || s.title != p.NativeTitle || s.manual) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if s.orcaID != p.OrcaID {
			s.orcaManaged = ""
		}
		status := syncPolicy(p.OrcaID, &p.OrcaTitle, p.Origin, s.orcaManaged, p.NativeTitle)
		result, err := tx.ExecContext(ctx, `UPDATE events SET orca_id=?,orca_base=?,orca_origin=?,native_handle=?,sync_status=?,updated=?
			WHERE id=? AND agent=? AND session_id=? AND revision=? AND title=? AND status='applied' AND sync_status='unbound'`,
			p.OrcaID, p.OrcaTitle, p.Origin, p.NativeHandle, status, e.now().UnixMilli(),
			p.ID, p.Agent, p.SessionID, p.Revision, p.NativeTitle)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET orca_id=?,orca_managed=?,
			session_kind=coalesce(nullif(?,''),session_kind) WHERE agent=? AND session_id=?`,
			p.OrcaID, s.orcaManaged, p.Kind, p.Agent, p.SessionID)
		return err == nil, err
	})
	if err == nil && changed {
		signal(e.syncWake)
	}
	return err
}
