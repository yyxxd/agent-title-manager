package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

const (
	recheckTurnInterval = 3
	recheckCooldown     = 3 * time.Minute
)

func hashTurnWindow(turns []turn) []string {
	hashes := make([]string, len(turns))
	for i, item := range turns {
		data, _ := json.Marshal(item)
		hashes[i] = hashText(data)[:16]
	}
	return hashes
}

func turnWindowOverlap(previous, next []string) int {
	if len(next) >= len(previous) && slices.Equal(previous, next[:len(previous)]) {
		return len(previous)
	}
	if len(previous) < 4 || len(next) < 4 {
		return 0
	}
	// 旧窗口的后缀须与新窗口的前缀重叠，且新窗口须包含新增消息。
	for overlap := min(len(previous), len(next)) - 1; overlap >= 2; overlap-- {
		if slices.Equal(previous[len(previous)-overlap:], next[:overlap]) {
			return overlap
		}
	}
	return 0
}

type titleReview struct {
	row, when        int64
	validationFailed bool
}

func latestTitleReview(ctx context.Context, tx *sql.Tx, p submission) (titleReview, error) {
	var review titleReview
	err := tx.QueryRowContext(ctx, `SELECT e.rowid,COALESCE(e.finalized,e.updated),
		e.status='failed' AND (e.failure_code IN ('title_too_long','title_category','title_separator','title_format')
			OR (e.failure_code='' AND (SELECT status FROM attempts WHERE event_id=e.id ORDER BY id DESC LIMIT 1)='completed'))
		FROM events e
		WHERE e.agent=? AND e.session_id=? AND e.native_only=0
		  AND (e.base=? OR (e.status='applied' AND e.title=?))
		  AND e.status IN ('applied','kept','failed','candidate','claimed')
		  AND EXISTS(SELECT 1 FROM attempts a WHERE a.event_id=e.id AND a.status='completed')
		ORDER BY e.rowid DESC LIMIT 1`, p.Agent, p.SessionID, p.CurrentTitle, p.CurrentTitle).
		Scan(&review.row, &review.when, &review.validationFailed)
	return review, err
}

func throttleNaming(ctx context.Context, tx *sql.Tx, p submission, s session, now int64, hashes []string, goalHash string) (keep bool, skippedTurn int, err error) {
	if p.CurrentTitle == "" || p.NativeOnly || s.manual || s.priorRevision == "" || s.priorProject != s.projectKey {
		return false, 0, nil
	}
	var baseline, previousHashes, previousGoal string
	var count int
	err = tx.QueryRowContext(ctx, `SELECT turns_hash,turn_count,turn_hashes,goal_hash FROM events
		WHERE agent=? AND session_id=? AND revision=? AND native_only=0`, p.Agent, p.SessionID, s.priorRevision).
		Scan(&baseline, &count, &previousHashes, &previousGoal)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	identical := false
	switch {
	case count <= 0:
		return false, 0, nil
	case previousGoal == "":
		// 旧记录只有整段哈希，只能用它验证前缀。
		if count > len(p.Turns) || turnsHash(p.OriginalGoal, p.Turns[:count]) != baseline {
			return false, 0, nil
		}
		identical = count == len(p.Turns)
	case previousGoal == goalHash:
		previous := strings.Split(previousHashes, ",")
		if turnWindowOverlap(previous, hashes) == 0 {
			return false, 0, nil
		}
		identical = slices.Equal(previous, hashes)
	default:
		return false, 0, nil
	}
	if identical && !p.SubstantiveActivity {
		return true, 0, nil
	}
	if p.ConfirmationOnly && !p.SubstantiveActivity {
		return false, 0, nil
	}
	review, err := latestTitleReview(ctx, tx, p)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if review.validationFailed || now-review.when >= recheckCooldown.Milliseconds() {
		return false, 0, nil
	}
	var skipped int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(throttle_skips),0) FROM events
		WHERE agent=? AND session_id=? AND base=? AND native_only=0 AND rowid>?`, p.Agent, p.SessionID, p.CurrentTitle, review.row).
		Scan(&skipped)
	if err != nil {
		return false, 0, err
	}
	if skipped >= recheckTurnInterval-1 {
		return false, 0, nil
	}
	return true, 1, nil
}
