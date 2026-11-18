package engine

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type listRequest struct {
	Limit int `json:"limit"`
}

type recordList struct {
	Items        []record        `json:"items"`
	TestAttempts []attemptRecord `json:"testAttempts"`
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
	UpdatedAt  string          `json:"updatedAt"`
	Attempts   []attemptRecord `json:"attempts"`
	Failure    *failureReason  `json:"failure,omitempty"`
}

type attemptRecord struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
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

const newestEvents = ` FROM events ORDER BY updated DESC,rowid DESC LIMIT ?`
const attemptFields = `SELECT event_id,provider,model,source,requested_tier,actual_tier,status,started,
	input_tokens,output_tokens,cached_tokens FROM attempts`

func (e *Engine) records(ctx context.Context, p listRequest) (recordList, error) {
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 0 || limit > 200 {
		return recordList{}, errors.New("记录条数必须在 1 到 200 之间")
	}
	local := e.now().Location()
	return transact(ctx, e.db, func(tx *sql.Tx) (recordList, error) {
		items, err := listRecords(ctx, tx, local, limit)
		if err != nil {
			return recordList{}, err
		}
		if err := listAttempts(ctx, tx, local, items, limit); err != nil {
			return recordList{}, err
		}
		for i := range items {
			items[i].addLegacyFailure()
		}
		tests := []attemptRecord{}
		err = readAttempts(ctx, tx, local, attemptFields+` WHERE event_id IS NULL ORDER BY id DESC LIMIT ?`,
			limit, func(_ string, item attemptRecord) { tests = append(tests, item) })
		return recordList{items, tests}, err
	})
}

func listRecords(ctx context.Context, tx *sql.Tx, local *time.Location, limit int) ([]record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,agent,session_id,revision,title,base,status,sync_status,updated,
		failure_code,failure_length,failure_limit`+newestEvents, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []record{}
	for rows.Next() {
		var item record
		var updated int64
		var reason failureReason
		err = rows.Scan(&item.ID, &item.Agent, &item.SessionID, &item.Revision, &item.Title, &item.BaseTitle, &item.Status,
			&item.SyncStatus, &updated, &reason.Code, &reason.Length, &reason.Limit)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt = formatTime(updated, local)
		item.Attempts = []attemptRecord{}
		if reason.Code != "" {
			item.Failure = &reason
		}
		items = append(items, item)
	}
	return items, rows.Err()
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

func listAttempts(ctx context.Context, tx *sql.Tx, local *time.Location, items []record, limit int) error {
	index := make(map[string]int, len(items))
	for i, item := range items {
		index[item.ID] = i
	}
	return readAttempts(ctx, tx, local, attemptFields+` WHERE event_id IN (SELECT id`+newestEvents+`) ORDER BY id`,
		limit, func(id string, item attemptRecord) {
			i := index[id]
			items[i].Attempts = append(items[i].Attempts, item)
		})
}

func readAttempts(ctx context.Context, tx *sql.Tx, local *time.Location, query string, limit int, add func(string, attemptRecord)) error {
	rows, err := tx.QueryContext(ctx, query, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item attemptRecord
		var id sql.NullString
		var started int64
		err = rows.Scan(&id, &item.Provider, &item.Model, &item.Source, &item.RequestedTier, &item.ActualTier,
			&item.Status, &started, &item.InputTokens, &item.OutputTokens, &item.CachedTokens)
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
