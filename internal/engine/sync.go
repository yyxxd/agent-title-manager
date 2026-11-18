package engine

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrSyncSkipped is returned by a syncer when its live safety checks disallow the write.
var ErrSyncSkipped = errors.New("Orca 同步已安全跳过")

// ErrSyncStale indicates the current native title differs from the synchronization target.
var ErrSyncStale = errors.New("原生标题已变化")

// SyncTarget identifies the native title that the host must re-read before any Orca write.
type SyncTarget struct {
	Agent, SessionID, NativeHandle, ExpectedTitle string
}

type syncTargetKey struct{}

// SyncTargetFromContext returns the native observation required by the host syncer.
func SyncTargetFromContext(ctx context.Context) (SyncTarget, bool) {
	target, ok := ctx.Value(syncTargetKey{}).(SyncTarget)
	return target, ok
}

type idRequest struct {
	ID string `json:"id"`
}

func syncPolicy(id string, expected *string, origin, managed, title string) string {
	switch {
	case id == "" || expected == nil:
		return "unbound"
	case *expected == title:
		return "unchanged"
	case managed != "" && *expected == managed:
		return "pending"
	case origin == "manual":
		return "protected"
	case origin == "auto" || *expected == "":
		return "pending"
	default:
		return "protected"
	}
}

type syncJob struct{ id, agent, sessionID, orcaID, expected, title, nativeHandle string }

func (e *Engine) nextSync() (*syncJob, error) {
	ctx := e.ctx
	return transact(ctx, e.db, func(tx *sql.Tx) (*syncJob, error) {
		now := e.now().UnixMilli()
		_, err := tx.ExecContext(ctx, `UPDATE events SET sync_status='stale',updated=?
			WHERE sync_status='pending' AND NOT EXISTS(SELECT 1 FROM sessions s WHERE s.agent=events.agent
			AND s.session_id=events.session_id AND s.title=events.title AND s.orca_id=events.orca_id)`, now)
		if err != nil {
			return nil, err
		}
		var job syncJob
		err = tx.QueryRowContext(ctx, `SELECT id,agent,session_id,orca_id,orca_base,title,native_handle FROM events
			WHERE sync_status='pending' ORDER BY updated LIMIT 1`).
			Scan(&job.id, &job.agent, &job.sessionID, &job.orcaID, &job.expected, &job.title, &job.nativeHandle)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE events SET sync_status='writing',updated=? WHERE id=?", now, job.id)
		return &job, err
	})
}

func (e *Engine) processSync() (bool, error) {
	job, err := e.nextSync()
	if err != nil || job == nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(e.ctx, 12*time.Second)
	ctx = context.WithValue(ctx, syncTargetKey{}, SyncTarget{job.agent, job.sessionID, job.nativeHandle, job.title})
	syncErr := e.syncer(ctx, job.orcaID, job.expected, job.title)
	cancel()
	// An interrupted write can be read back and retried without invoking a model.
	if e.ctx.Err() != nil {
		return true, nil
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = transact(ctx, e.db, func(tx *sql.Tx) (bool, error) {
		status := "synced"
		if errors.Is(syncErr, ErrSyncStale) {
			status = "stale"
		} else if errors.Is(syncErr, ErrSyncSkipped) {
			status = "skipped"
		} else if syncErr != nil {
			status = "failed"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE events SET sync_status=?,updated=? WHERE id=?", status, e.now().UnixMilli(), job.id); err != nil {
			return false, err
		}
		if syncErr == nil {
			_, err := tx.ExecContext(ctx, `UPDATE sessions SET orca_managed=? WHERE agent=? AND session_id=? AND orca_id=?`,
				job.title, job.agent, job.sessionID, job.orcaID)
			return true, err
		}
		return true, nil
	})
	return true, err
}

func (e *Engine) resync(ctx context.Context, p idRequest, adopt bool) (accepted, error) {
	if e.syncer == nil {
		return accepted{}, errors.New("Orca 同步服务未连接")
	}
	if p.ID == "" || !validString(p.ID, 128) {
		return accepted{}, errors.New("记录 ID 无效")
	}
	status := "failed"
	if adopt {
		status = "protected"
	}
	result, err := e.db.ExecContext(ctx, `UPDATE events SET sync_status='pending', updated=?
		WHERE id=? AND sync_status=? AND EXISTS(SELECT 1 FROM sessions s WHERE s.agent=events.agent
		AND s.session_id=events.session_id AND s.title=events.title AND s.orca_id=events.orca_id)`, e.now().UnixMilli(), p.ID, status)
	if err != nil {
		return accepted{}, err
	}
	n, err := result.RowsAffected()
	if n > 0 {
		signal(e.syncWake)
	}
	return accepted{n > 0}, err
}
