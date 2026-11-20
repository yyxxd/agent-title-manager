package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func turnsHash(goal string, turns []turn) string {
	data, _ := json.Marshal(struct {
		Goal  string
		Turns []turn
	}{truncateText(goal, 800), turns})
	return hashText(data)
}

func confirmationSkips(ctx context.Context, tx *sql.Tx, p submission, s session) (int, error) {
	if !p.ConfirmationOnly || p.SubstantiveActivity || p.NativeOnly || s.manual || s.priorRevision == "" ||
		s.priorProject != s.projectKey || p.CurrentTitle == "" {
		return 0, nil
	}
	var baseline, previousHashes, previousGoal, status string
	var count, skips int
	var managed bool
	err := tx.QueryRowContext(ctx, `SELECT prior.turns_hash,prior.turn_count,prior.confirmation_skips,prior.turn_hashes,prior.goal_hash,prior.status,
		EXISTS(SELECT 1 FROM events AS applied WHERE applied.agent=prior.agent AND applied.session_id=prior.session_id
			AND applied.status='applied' AND applied.title=?)
		FROM events AS prior WHERE prior.agent=? AND prior.session_id=? AND prior.revision=? AND prior.native_only=0
		AND prior.status IN ('applied','kept','failed') AND prior.title=?`,
		p.CurrentTitle, p.Agent, p.SessionID, s.priorRevision, p.CurrentTitle).
		Scan(&baseline, &count, &skips, &previousHashes, &previousGoal, &status, &managed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !managed || count <= 0 {
		return 0, nil
	}
	review, err := latestTitleReview(ctx, tx, p)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if !review.validationFailed && (status == "failed" || skips >= 2) {
		return 0, nil
	}
	nextSkip := min(skips+1, 2)
	if count < len(p.Turns) && turnsHash(p.OriginalGoal, p.Turns[:count]) == baseline && pureConfirmation(p.Turns[count:]) {
		return nextSkip, nil
	}
	if review.validationFailed && previousGoal != "" && previousGoal == hashText([]byte(p.OriginalGoal)) {
		overlap := turnWindowOverlap(strings.Split(previousHashes, ","), hashTurnWindow(p.Turns))
		if overlap > 0 && pureConfirmation(p.Turns[overlap:]) {
			return nextSkip, nil
		}
	}
	return 0, nil
}

func pureConfirmation(extra []turn) bool {
	if len(extra) == 0 || extra[0].Role != "user" {
		return false
	}
	for _, item := range extra[1:] {
		if item.Role != "assistant" {
			return false
		}
	}
	switch strings.ToLower(strings.Trim(extra[0].Text, " \r\n\t.!。！")) {
	case "好", "好的", "可以", "收到", "谢谢", "嗯嗯", "ok", "okay", "thanks", "thank you", "继续", "continue":
		return true
	default:
		return false
	}
}
