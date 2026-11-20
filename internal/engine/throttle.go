package engine

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	recheckTurnInterval = 3
	recheckCooldown     = 3 * time.Minute
)

func throttleNaming(ctx context.Context, tx *sql.Tx, p submission, s session, now int64) (keep bool, skippedTurn int, err error) {
	if p.CurrentTitle == "" || p.NativeOnly || s.manual || p.ConfirmationOnly || s.priorRevision == "" || s.priorProject != s.projectKey {
		return false, 0, nil
	}
	var baseline string
	var count int
	err = tx.QueryRowContext(ctx, `SELECT turns_hash,turn_count FROM events
		WHERE agent=? AND session_id=? AND revision=? AND native_only=0`, p.Agent, p.SessionID, s.priorRevision).
		Scan(&baseline, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	// 对被编辑或滚动截断的对话上下文重新评估，防止把话题变化误认为续写。
	if count <= 0 || count > len(p.Turns) || turnsHash(p.OriginalGoal, p.Turns[:count]) != baseline {
		return false, 0, nil
	}
	var reviewed, when int64
	err = tx.QueryRowContext(ctx, `SELECT e.rowid,COALESCE(e.finalized,e.updated) FROM events e
		WHERE e.agent=? AND e.session_id=? AND e.native_only=0
		  AND (e.base=? OR (e.status='applied' AND e.title=?))
		  AND e.status IN ('applied','kept','failed','candidate','claimed')
		  AND EXISTS(SELECT 1 FROM attempts a WHERE a.event_id=e.id AND a.status='completed')
		ORDER BY e.rowid DESC LIMIT 1`, p.Agent, p.SessionID, p.CurrentTitle, p.CurrentTitle).Scan(&reviewed, &when)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if now-when >= recheckCooldown.Milliseconds() {
		return false, 0, nil
	}
	if count == len(p.Turns) {
		return true, 0, nil
	}
	var skipped int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(throttle_skips),0) FROM events
		WHERE agent=? AND session_id=? AND base=? AND native_only=0 AND rowid>?`, p.Agent, p.SessionID, p.CurrentTitle, reviewed).
		Scan(&skipped)
	if err != nil {
		return false, 0, err
	}
	if skipped >= recheckTurnInterval-1 {
		return false, 0, nil
	}
	return true, 1, nil
}
