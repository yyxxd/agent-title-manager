package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("atm_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if value, ok := args[0].(string); ok {
			return strings.ToLower(value), nil
		}
		return nil, nil
	})
}

type listRequest struct {
	Limit   int           `json:"limit"`
	Filter  string        `json:"filter"`
	Query   string        `json:"query"`
	Before  *recordCursor `json:"before"`
	Grouped bool          `json:"grouped"`
}

type recordCursor struct {
	Updated int64 `json:"updated"`
	RowID   int64 `json:"rowId"`
}

type recordCounts struct {
	Total      int `json:"total"`
	Unread     int `json:"unread"`
	Attention  int `json:"attention"`
	Failed     int `json:"failed"`
	SyncFailed int `json:"syncFailed"`
	Protected  int `json:"protected"`
}

type recordList struct {
	Items        []record        `json:"items"`
	TestAttempts []attemptRecord `json:"testAttempts"`
	Counts       recordCounts    `json:"counts"`
	Next         *recordCursor   `json:"next,omitempty"`
}

type record struct {
	ID         string          `json:"id"`
	Agent      string          `json:"agent"`
	SessionID  string          `json:"sessionId"`
	Revision   string          `json:"revision"`
	Title      string          `json:"title"`
	BaseTitle  string          `json:"baseTitle"`
	Status     string          `json:"status"`
	SyncStatus string          `json:"syncStatus"`
	SyncError  string          `json:"syncError,omitempty"`
	OrcaOrigin string          `json:"orcaTitleOrigin,omitempty"`
	CanAdopt   bool            `json:"canAdopt"`
	UpdatedAt  string          `json:"updatedAt"`
	Attempts   []attemptRecord `json:"attempts"`
	Failure    *failureReason  `json:"failure,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	SessionKey string          `json:"sessionKey,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Locked     bool            `json:"locked,omitempty"`
	Protected  bool            `json:"protected,omitempty"`
	ChangedAt  string          `json:"changedAt,omitempty"`
	CheckedAt  string          `json:"checkedAt,omitempty"`
	Changes    []recordChange  `json:"changes,omitempty"`
	cursor     recordCursor
}

type attemptRecord struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ActualModel   string `json:"actualModel,omitempty"`
	Source        string `json:"source"`
	RequestedTier string `json:"requestedTier"`
	ActualTier    string `json:"actualTier"`
	Status        string `json:"status"`
	StartedAt     string `json:"startedAt"`
	InputTokens   *int64 `json:"inputTokens"`
	OutputTokens  *int64 `json:"outputTokens"`
	CachedTokens  *int64 `json:"cachedTokens"`
	UsageKnown    bool   `json:"usageKnown"`
}

const recordAdoptable = `(e.sync_status='protected' AND e.orca_id<>'' AND e.orca_base IS NOT NULL AND ` + currentSyncSession + `)`
const recordAttention = `(e.status IN ('failed','needs_review') OR e.sync_status IN ('failed','blocked') OR ` + recordAdoptable + `)`
const recordUnread = `(e.reminder_read=0 AND ` + recordAttention + `)`
const attemptFields = `SELECT event_id,provider,model,source,requested_tier,actual_tier,status,started,
	input_tokens,output_tokens,cached_tokens,actual_model FROM attempts`

func recordSearch(query string) (string, []any) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return " WHERE 1", nil
	}
	return ` WHERE (instr(atm_lower(e.title || ' ' || e.base || ' ' || e.agent || ' ' || e.session_id || ' ' || e.sync_error),?)>0
		OR (e.agent='claude' AND instr('claude code',?)>0)
		OR EXISTS(SELECT 1 FROM attempts a WHERE a.event_id=e.id AND
			(instr(atm_lower(a.model),?)>0 OR instr(atm_lower(a.actual_model),?)>0)))`, []any{query, query, query, query}
}

func (e *Engine) records(ctx context.Context, p listRequest, markRead bool) (recordList, error) {
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 0 || limit > 200 {
		return recordList{}, errors.New("记录条数必须在 1 到 200 之间")
	}
	if len(p.Query) > 2048 || (p.Filter != "" && p.Filter != "all" && p.Filter != "attention" && !validAgent.MatchString(p.Filter)) ||
		(p.Before != nil && (p.Before.RowID <= 0 || p.Before.Updated < 0)) {
		return recordList{}, errors.New("记录筛选参数无效")
	}
	local := e.now().Location()
	return transact(ctx, e.db, func(tx *sql.Tx) (recordList, error) {
		if markRead {
			if _, err := tx.ExecContext(ctx, `UPDATE events AS e SET reminder_read=1 WHERE `+recordUnread); err != nil {
				return recordList{}, err
			}
		}
		if p.Grouped {
			return groupedRecords(ctx, tx, local, p, limit)
		}
		where, args := recordSearch(p.Query)
		var result recordList
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE `+recordUnread+`),COUNT(*) FILTER (WHERE `+recordAttention+`),
			COUNT(*) FILTER (WHERE e.status='failed'),COUNT(*) FILTER (WHERE e.sync_status='failed'),
			COUNT(*) FILTER (WHERE `+recordAdoptable+`) FROM events e`+where, args...).
			Scan(&result.Counts.Total, &result.Counts.Unread, &result.Counts.Attention, &result.Counts.Failed, &result.Counts.SyncFailed, &result.Counts.Protected)
		if err != nil {
			return recordList{}, err
		}
		switch p.Filter {
		case "", "all":
		case "attention":
			where += " AND " + recordAttention
		default:
			where += " AND e.agent=?"
			args = append(args, p.Filter)
		}
		if p.Before != nil {
			where += " AND (e.updated,e.rowid)<(?,?)"
			args = append(args, p.Before.Updated, p.Before.RowID)
		}
		items, err := listRecords(ctx, tx, local, where, append(args, limit+1))
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
		result.Items = items
		result.TestAttempts = []attemptRecord{}
		err = readAttempts(ctx, tx, local, attemptFields+` WHERE event_id IS NULL ORDER BY id DESC LIMIT ?`,
			[]any{limit}, func(_ string, item attemptRecord) { result.TestAttempts = append(result.TestAttempts, item) })
		return result, err
	})
}

func listRecords(ctx context.Context, tx *sql.Tx, local *time.Location, where string, args []any) ([]record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,agent,session_id,revision,title,base,status,sync_status,sync_error,updated,
		failure_code,failure_length,failure_limit,decision_reason,rowid,orca_origin,`+recordAdoptable+`
		FROM events e`+where+` ORDER BY updated DESC,rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []record{}
	for rows.Next() {
		var item record
		if err := scanRecord(rows, &item, local); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRecord(rows *sql.Rows, item *record, local *time.Location, extra ...any) error {
	var updated int64
	var reason failureReason
	fields := []any{&item.ID, &item.Agent, &item.SessionID, &item.Revision, &item.Title, &item.BaseTitle, &item.Status,
		&item.SyncStatus, &item.SyncError, &updated, &reason.Code, &reason.Length, &reason.Limit,
		&item.Reason, &item.cursor.RowID, &item.OrcaOrigin, &item.CanAdopt}
	if err := rows.Scan(append(fields, extra...)...); err != nil {
		return err
	}
	item.UpdatedAt = formatTime(updated, local)
	item.cursor.Updated = updated
	item.Attempts = []attemptRecord{}
	if reason.Code != "" {
		item.Failure = &reason
	}
	return nil
}

func (item *record) addLegacyFailure() {
	if item.Status != "failed" || item.Failure != nil {
		return
	}
	code := failureUnrecorded
	if len(item.Attempts) > 0 && item.Attempts[len(item.Attempts)-1].Status == "completed" {
		// 旧版只有模型完成后未通过标题校验，才会把该命名事件标记为失败。
		code = failureLegacyTitle
	}
	item.Failure = &failureReason{Code: code}
}

func listAttempts(ctx context.Context, tx *sql.Tx, local *time.Location, items []record) error {
	if len(items) == 0 {
		return nil
	}
	index := make(map[string]int, len(items))
	params := make([]any, len(items))
	for i, item := range items {
		index[item.ID] = i
		params[i] = item.ID
	}
	return readAttempts(ctx, tx, local, attemptFields+` WHERE event_id IN (?`+strings.Repeat(",?", len(params)-1)+`) ORDER BY id`,
		params, func(id string, item attemptRecord) {
			i := index[id]
			items[i].Attempts = append(items[i].Attempts, item)
		})
}

func readAttempts(ctx context.Context, tx *sql.Tx, local *time.Location, query string, args []any, add func(string, attemptRecord)) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item attemptRecord
		var id sql.NullString
		var started int64
		err = rows.Scan(&id, &item.Provider, &item.Model, &item.Source, &item.RequestedTier, &item.ActualTier,
			&item.Status, &started, &item.InputTokens, &item.OutputTokens, &item.CachedTokens, &item.ActualModel)
		if err != nil {
			return err
		}
		if item.ActualTier == "" {
			item.ActualTier = "unknown"
		}
		item.StartedAt = formatTime(started, local)
		item.UsageKnown = item.InputTokens != nil && item.OutputTokens != nil && item.CachedTokens != nil
		add(id.String, item)
	}
	return rows.Err()
}
