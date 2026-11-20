package engine

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

type dateRequest struct {
	Date string `json:"date"`
}

type usageTotals struct {
	Attempts     int64  `json:"attempts"`
	TestAttempts int64  `json:"testAttempts"`
	InputTokens  *int64 `json:"inputTokens"`
	OutputTokens *int64 `json:"outputTokens"`
	CachedTokens *int64 `json:"cachedTokens"`
	InputKnown   int64  `json:"inputKnown"`
	OutputKnown  int64  `json:"outputKnown"`
	CachedKnown  int64  `json:"cachedKnown"`
	UnknownUsage int64  `json:"unknownUsage"`
}

type totals struct {
	usageTotals
	Renames   int64 `json:"renames"`
	Kept      int64 `json:"kept"`
	ModelKept int64 `json:"modelKept"`
}

type agentTotals struct {
	Agent string `json:"agent"`
	totals
}

type modelTotals struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	usageTotals
}

type dailyStats struct {
	Date  string `json:"date"`
	Since string `json:"since"`
	totals
	ByAgent     []agentTotals `json:"byAgent"`
	ByModel     []modelTotals `json:"byModel"`
	NamingUsage usageTotals   `json:"namingUsage"`
}

const statsQuery = `
WITH outcomes AS (
	SELECT agent,'' AS provider,'' AS model,0 AS attempts,0 AS tests,(status='applied') AS renames,
		(status='kept') AS kept,(status='kept' AND decision_reason IN
			('` + decisionModelKeep + `','` + decisionKeepGoal + `','` + decisionKeepProgress + `','` + decisionKeepUnknown + `')) AS model_kept,
		NULL AS input,NULL AS output,NULL AS cached,0 AS unknown
	FROM events WHERE finalized>=? AND finalized<?
	UNION ALL
	SELECT agent,provider,CASE WHEN provider='claude' THEN actual_model ELSE model END,1,event_id IS NULL,0,0,0,input_tokens,output_tokens,cached_tokens,
		(input_tokens IS NULL OR output_tokens IS NULL OR cached_tokens IS NULL)
	FROM attempts WHERE started>=? AND started<?
)
SELECT agent,provider,model,SUM(attempts),SUM(tests),SUM(renames),SUM(kept),SUM(model_kept),SUM(input),SUM(output),SUM(cached),
	COUNT(input),COUNT(output),COUNT(cached),SUM(unknown)
FROM outcomes GROUP BY agent,provider,model,tests
`

func (e *Engine) daily(ctx context.Context, p dateRequest) (dailyStats, error) {
	now := e.now()
	local := now.Location()
	date := p.Date
	if date == "" {
		date = now.Format(time.DateOnly)
	}
	start, err := time.ParseInLocation(time.DateOnly, date, local)
	if err != nil {
		return dailyStats{}, errors.New("日期必须为 YYYY-MM-DD")
	}
	end := start.AddDate(0, 0, 1)
	return e.statsBetween(ctx, start, end, date)
}

type dateRangeRequest struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func (e *Engine) dateRange(ctx context.Context, p dateRangeRequest) (dailyStats, error) {
	loc := e.now().Location()
	start, fromErr := time.ParseInLocation(time.DateOnly, p.Start, loc)
	last, toErr := time.ParseInLocation(time.DateOnly, p.End, loc)
	if fromErr != nil || toErr != nil {
		return dailyStats{}, errors.New("开始和结束日期必须为 YYYY-MM-DD")
	}
	if last.Before(start) {
		return dailyStats{}, errors.New("结束日期不能早于开始日期")
	}
	return e.statsBetween(ctx, start, last.AddDate(0, 0, 1), p.End)
}

func (e *Engine) statsBetween(ctx context.Context, start, end time.Time, date string) (dailyStats, error) {
	var since int64
	if err := e.db.QueryRowContext(ctx, "SELECT since FROM settings WHERE id=1").Scan(&since); err != nil {
		return dailyStats{}, err
	}
	reply := dailyStats{Date: date, Since: formatTime(since, start.Location()), ByAgent: []agentTotals{}, ByModel: []modelTotals{}}
	rows, err := e.db.QueryContext(ctx, statsQuery, start.UnixMilli(), end.UnixMilli(), start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return dailyStats{}, err
	}
	defer rows.Close()
	return collectDaily(rows, reply)
}

func collectDaily(rows *sql.Rows, reply dailyStats) (dailyStats, error) {
	agents := map[string]totals{}
	models := map[[2]string]usageTotals{}
	for rows.Next() {
		var agent, provider, model string
		var group totals
		err := rows.Scan(&agent, &provider, &model, &group.Attempts, &group.TestAttempts, &group.Renames, &group.Kept, &group.ModelKept,
			&group.InputTokens, &group.OutputTokens, &group.CachedTokens, &group.InputKnown, &group.OutputKnown,
			&group.CachedKnown, &group.UnknownUsage)
		if err != nil {
			return dailyStats{}, err
		}
		t := agents[agent]
		t.add(group)
		agents[agent] = t
		reply.totals.add(group)
		if group.TestAttempts == 0 {
			reply.NamingUsage.add(group.usageTotals)
		}
		if provider != "" {
			key := [2]string{provider, model}
			m := models[key]
			m.add(group.usageTotals)
			models[key] = m
		}
	}
	for agent, t := range agents {
		reply.ByAgent = append(reply.ByAgent, agentTotals{agent, t})
	}
	for key, u := range models {
		reply.ByModel = append(reply.ByModel, modelTotals{key[0], key[1], u})
	}
	sort.Slice(reply.ByAgent, func(i, j int) bool { return reply.ByAgent[i].Agent < reply.ByAgent[j].Agent })
	sort.Slice(reply.ByModel, func(i, j int) bool {
		a, b := reply.ByModel[i], reply.ByModel[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
	return reply, rows.Err()
}

func (s *totals) add(a totals) {
	s.Renames += a.Renames
	s.Kept += a.Kept
	s.ModelKept += a.ModelKept
	s.usageTotals.add(a.usageTotals)
}

func (s *usageTotals) add(a usageTotals) {
	s.Attempts += a.Attempts
	s.TestAttempts += a.TestAttempts
	s.InputTokens = addKnown(s.InputTokens, a.InputTokens)
	s.OutputTokens = addKnown(s.OutputTokens, a.OutputTokens)
	s.CachedTokens = addKnown(s.CachedTokens, a.CachedTokens)
	s.InputKnown += a.InputKnown
	s.OutputKnown += a.OutputKnown
	s.CachedKnown += a.CachedKnown
	s.UnknownUsage += a.UnknownUsage
}

func addKnown(acc, value *int64) *int64 {
	if value == nil {
		return acc
	}
	if acc == nil {
		return value
	}
	sum := *acc + *value
	return &sum
}

func formatTime(ms int64, local *time.Location) string {
	return time.UnixMilli(ms).In(local).Format(time.RFC3339Nano)
}
