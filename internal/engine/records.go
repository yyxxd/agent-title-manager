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
	Items []record `json:"items"`
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
}

type attemptRecord struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Source        string `json:"source"`
	RequestedTier string `json:"requestedTier"`
	ActualTier    string `json:"actualTier"`
	Status        string `json:"status"`
	StartedAt     string `json:"startedAt"`
	InputTokens   int64  `json:"inputTokens"`
	OutputTokens  int64  `json:"outputTokens"`
	CachedTokens  int64  `json:"cachedTokens"`
	UsageKnown    bool   `json:"usageKnown"`
}

const newestEvents = ` FROM events ORDER BY updated DESC,rowid DESC LIMIT ?`

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
		return recordList{items}, listAttempts(ctx, tx, local, items, limit)
	})
}

func listRecords(ctx context.Context, tx *sql.Tx, local *time.Location, limit int) ([]record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,agent,session_id,revision,title,base,status,sync_status,updated`+newestEvents, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []record{}
	for rows.Next() {
		var item record
		var updated int64
		err = rows.Scan(&item.ID, &item.Agent, &item.SessionID, &item.Revision, &item.Title, &item.BaseTitle, &item.Status, &item.SyncStatus, &updated)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt = formatTime(updated, local)
		item.Attempts = []attemptRecord{}
		items = append(items, item)
	}
	return items, rows.Err()
}

func listAttempts(ctx context.Context, tx *sql.Tx, local *time.Location, items []record, limit int) error {
	index := make(map[string]int, len(items))
	for i, item := range items {
		index[item.ID] = i
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_id,provider,model,source,requested_tier,actual_tier,status,started,
		input_tokens,output_tokens,cached_tokens FROM attempts WHERE event_id IN (SELECT id`+newestEvents+`) ORDER BY id`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item attemptRecord
		var id string
		var started int64
		var input, output, cached sql.NullInt64
		err = rows.Scan(&id, &item.Provider, &item.Model, &item.Source, &item.RequestedTier, &item.ActualTier, &item.Status, &started, &input, &output, &cached)
		if err != nil {
			return err
		}
		if item.ActualTier == "" {
			item.ActualTier = "unknown"
		}
		item.StartedAt = formatTime(started, local)
		item.InputTokens, item.OutputTokens, item.CachedTokens = input.Int64, output.Int64, cached.Int64
		item.UsageKnown = input.Valid && output.Valid && cached.Valid
		i := index[id]
		items[i].Attempts = append(items[i].Attempts, item)
	}
	return rows.Err()
}
