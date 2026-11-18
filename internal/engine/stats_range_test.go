package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDateRangeIncludesBothLocalDaysAndPreservesUsage(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	local := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, local)
	end := start.AddDate(0, 1, 0)
	statsEngine := &Engine{db: e.db, now: func() time.Time { return end }}
	const insert = `INSERT INTO attempts(agent,provider,model,source,requested_tier,status,started,input_tokens,cached_tokens)
		VALUES('pi','api','range-test','openai_compatible','default','completed',?,5,?)`
	for i, at := range []time.Time{start.Add(-time.Millisecond), start, end.Add(-time.Millisecond), end} {
		var cached any
		if i == 1 {
			cached = 0
		}
		if _, err := e.db.Exec(insert, at.UnixMilli(), cached); err != nil {
			t.Fatal(err)
		}
	}
	stats := call[dailyStats](t, statsEngine, "stats.range", dateRangeRequest{"2026-10-01", "2026-10-31"})
	if stats.Attempts != 2 || !hasCount(stats.InputTokens, 10) || stats.OutputTokens != nil ||
		stats.CachedKnown != 1 || !hasCount(stats.CachedTokens, 0) || stats.UnknownUsage != 2 ||
		len(stats.ByAgent) != 1 || stats.ByAgent[0].Attempts != 2 ||
		len(stats.ByModel) != 1 || stats.ByModel[0].Attempts != 2 {
		t.Fatalf("unexpected range totals: %+v", stats)
	}
	for _, body := range []string{
		`{"start":"2026-10-31","end":"2026-10-01"}`,
		`{"start":"2026-10-01","end":"2026-10-32"}`,
		`{"start":"2026-10-01"}`,
	} {
		if _, err := statsEngine.Handle(context.Background(), "stats.range", json.RawMessage(body)); err == nil {
			t.Fatalf("accepted invalid range: %s", body)
		}
	}
}
