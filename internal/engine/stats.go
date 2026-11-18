package engine

import (
	"context"
	"errors"
	"sort"
	"time"
)

type dateRequest struct {
	Date string `json:"date"`
}

type totals struct {
	Attempts     int64 `json:"attempts"`
	Renames      int64 `json:"renames"`
	Kept         int64 `json:"kept"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`
	UnknownUsage int64 `json:"unknownUsage"`
}

type agentTotals struct {
	Agent string `json:"agent"`
	totals
}

type dailyStats struct {
	Date  string `json:"date"`
	Since string `json:"since"`
	totals
	ByAgent []agentTotals `json:"byAgent"`
}

const dailyQuery = `
WITH outcomes AS (
	SELECT agent,0 AS attempts,(status='applied') AS renames,(status='kept') AS kept,0 AS input,0 AS output,0 AS cached,0 AS unknown
	FROM events WHERE finalized>=? AND finalized<?
	UNION ALL
	SELECT agent,1,0,0,COALESCE(input_tokens,0),COALESCE(output_tokens,0),COALESCE(cached_tokens,0),
		(input_tokens IS NULL OR output_tokens IS NULL OR cached_tokens IS NULL)
	FROM attempts WHERE started>=? AND started<?
)
SELECT agent,SUM(attempts),SUM(renames),SUM(kept),SUM(input),SUM(output),SUM(cached),SUM(unknown)
FROM outcomes GROUP BY agent
`

func (e *Engine) daily(ctx context.Context, p dateRequest) (dailyStats, error) {
	local := e.now().Location()
	date := p.Date
	if date == "" {
		date = e.now().Format(time.DateOnly)
	}
	start, err := time.ParseInLocation(time.DateOnly, date, local)
	if err != nil {
		return dailyStats{}, errors.New("日期必须为 YYYY-MM-DD")
	}
	end := start.AddDate(0, 0, 1)
	var since int64
	if err = e.db.QueryRowContext(ctx, "SELECT since FROM settings WHERE id=1").Scan(&since); err != nil {
		return dailyStats{}, err
	}
	reply := dailyStats{Date: date, Since: formatTime(since, local), ByAgent: []agentTotals{}}
	rows, err := e.db.QueryContext(ctx, dailyQuery, start.UnixMilli(), end.UnixMilli(), start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return dailyStats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a agentTotals
		if err = rows.Scan(&a.Agent, &a.Attempts, &a.Renames, &a.Kept, &a.InputTokens, &a.OutputTokens, &a.CachedTokens, &a.UnknownUsage); err != nil {
			return dailyStats{}, err
		}
		reply.add(a)
	}
	sort.Slice(reply.ByAgent, func(i, j int) bool { return reply.ByAgent[i].Agent < reply.ByAgent[j].Agent })
	return reply, rows.Err()
}

func (s *dailyStats) add(a agentTotals) {
	s.ByAgent = append(s.ByAgent, a)
	s.Attempts += a.Attempts
	s.Renames += a.Renames
	s.Kept += a.Kept
	s.InputTokens += a.InputTokens
	s.OutputTokens += a.OutputTokens
	s.CachedTokens += a.CachedTokens
	s.UnknownUsage += a.UnknownUsage
}

func formatTime(ms int64, local *time.Location) string {
	return time.UnixMilli(ms).In(local).Format(time.RFC3339Nano)
}
