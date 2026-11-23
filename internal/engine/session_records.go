package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"time"
)

type recordChange struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	Title  string `json:"title"`
	Source string `json:"source"`
	At     string `json:"at,omitempty"`
	Found  bool   `json:"found,omitempty"`
}

const renameEvidence = `e.title<>e.base AND e.status IN ('applied','manual_change')`

func groupedRecords(ctx context.Context, tx *sql.Tx, local *time.Location, p listRequest, limit int) (recordList, error) {
	where, args := recordSearch(p.Query)
	cte := `WITH matches AS (
		SELECT DISTINCT e.agent,e.session_id FROM events e` + where + `
	), summary AS (
		SELECT e.agent,e.session_id,MIN(e.rowid) AS first_row,MAX(e.created) AS checked,
			MAX(e.finalized) FILTER (WHERE ` + renameEvidence + ` AND e.status='applied') AS changed,
			MAX(e.status='manual_change' AND e.title<>e.base) AS discovered,
			MAX(` + recordUnread + `) AS unread,MAX(` + recordAttention + `) AS attention,
			MAX(e.status='failed') AS failed,MAX(e.sync_status='failed') AS sync_failed,
			MAX(` + recordAdoptable + `) AS protected
		FROM events e JOIN matches USING(agent,session_id) GROUP BY e.agent,e.session_id
	)`
	result := recordList{Items: []record{}, TestAttempts: []attemptRecord{}}
	err := tx.QueryRowContext(ctx, cte+` SELECT COUNT(*),coalesce(SUM(unread),0),coalesce(SUM(attention),0),
		coalesce(SUM(failed),0),coalesce(SUM(sync_failed),0),coalesce(SUM(protected),0) FROM summary`, args...).
		Scan(&result.Counts.Total, &result.Counts.Unread, &result.Counts.Attention, &result.Counts.Failed, &result.Counts.SyncFailed, &result.Counts.Protected)
	if err != nil {
		return recordList{}, err
	}
	items, err := groupedPage(ctx, tx, local, cte, args, p, limit+1)
	if err != nil {
		return recordList{}, err
	}
	if len(items) > limit {
		items = items[:limit]
		result.Next = &items[limit-1].cursor
	}
	if err := listAttempts(ctx, tx, local, items); err != nil {
		return recordList{}, err
	}
	for i := range items {
		items[i].addLegacyFailure()
	}
	if err := listChanges(ctx, tx, local, items); err != nil {
		return recordList{}, err
	}
	result.Items = items
	err = readAttempts(ctx, tx, local, attemptFields+` WHERE event_id IS NULL ORDER BY id DESC LIMIT ?`,
		[]any{limit}, func(_ string, item attemptRecord) { result.TestAttempts = append(result.TestAttempts, item) })
	return result, err
}

func groupedPage(ctx context.Context, tx *sql.Tx, local *time.Location, cte string, args []any, p listRequest, limit int) ([]record, error) {
	where, order, preferred := " WHERE 1", "coalesce(g.changed,0)", "e.rowid IS NULL"
	switch p.Filter {
	case "all":
		order = "g.checked"
	case "attention":
		where, preferred, order = " WHERE g.attention=1", recordAttention, "g.checked"
	case "locked":
		where = " WHERE s.protection_reason='manual_locked'"
	case "", "changes":
		where = " WHERE (g.changed IS NOT NULL OR g.discovered=1)"
	default:
		where += " AND g.agent=?"
		args = append(args, p.Filter)
	}
	if p.Before != nil {
		where += " AND (" + order + ",g.first_row)<(?,?)"
		args = append(args, p.Before.Updated, p.Before.RowID)
	}
	rows, err := tx.QueryContext(ctx, cte+`
		SELECT e.id,g.agent,g.session_id,e.revision,s.title,e.base,e.status,e.sync_status,e.sync_error,e.updated,
			e.failure_code,e.failure_length,e.failure_limit,e.decision_reason,g.first_row,e.orca_origin,`+recordAdoptable+`,
			s.orca_id,s.protection_reason,s.session_kind,g.changed,g.checked,`+order+`,
			CASE WHEN e.status='needs_review' THEN e.title ELSE '' END
		FROM summary g JOIN sessions s USING(agent,session_id)
		JOIN events e ON e.rowid=(
			SELECT rowid FROM events e WHERE e.agent=g.agent AND e.session_id=g.session_id
			ORDER BY (`+preferred+`) DESC,e.created DESC,e.rowid DESC LIMIT 1
		)`+where+` ORDER BY `+order+` DESC,g.first_row DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []record{}
	for rows.Next() {
		var item record
		var orcaID, protection string
		var changed sql.NullInt64
		var checked, sortTime int64
		if err := scanRecord(rows, &item, local, &orcaID, &protection, &item.Kind, &changed, &checked, &sortTime, &item.SuggestedTitle); err != nil {
			return nil, err
		}
		item.SessionKey = base64.RawURLEncoding.EncodeToString([]byte(item.Agent + "\x00" + item.SessionID))
		item.Locked = protection == decisionManual
		item.Protected = protection == decisionProtected
		item.CheckedAt = formatTime(checked, local)
		if changed.Valid {
			item.ChangedAt = formatTime(changed.Int64, local)
		}
		if item.Kind == "" && orcaID != "" {
			item.Kind = "orca"
		}
		item.cursor.Updated = sortTime
		items = append(items, item)
	}
	return items, rows.Err()
}

func listChanges(ctx context.Context, tx *sql.Tx, local *time.Location, items []record) error {
	if len(items) == 0 {
		return nil
	}
	index := make(map[[2]string]int, len(items))
	args := make([]any, 0, 2*len(items))
	for i, item := range items {
		index[[2]string{item.Agent, item.SessionID}] = i
		args = append(args, item.Agent, item.SessionID)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT agent,session_id,id,base,title,status,finalized FROM events e
		WHERE (agent,session_id) IN (VALUES `+strings.TrimSuffix(strings.Repeat("(?,?),", len(items)), ",")+`)
			AND `+renameEvidence+`
		ORDER BY coalesce(finalized,created) DESC,rowid DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var agent, sessionID, status string
		var at sql.NullInt64
		var change recordChange
		if err := rows.Scan(&agent, &sessionID, &change.ID, &change.From, &change.Title, &status, &at); err != nil {
			return err
		}
		change.Source = "auto"
		if status == "manual_change" {
			change.Source = "manual"
			change.Found = true
		}
		if at.Valid {
			change.At = formatTime(at.Int64, local)
		}
		item := &items[index[[2]string{agent, sessionID}]]
		if n := len(item.Changes); n == 0 || item.Changes[n-1].Title != change.Title ||
			item.Changes[n-1].From != change.From {
			item.Changes = append(item.Changes, change)
		}
	}
	return rows.Err()
}
