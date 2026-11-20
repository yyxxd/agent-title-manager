package engine

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyProtectionKeepsHistoricalReasonsUnknown(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8, schemaV9,
		`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('codex','session-test','old','已有标题',1);
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,finalized)
		VALUES('past','codex','session-test','old','已有标题','已有标题','kept','','',1,1,1);`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir, nil)
	for _, step := range []struct{ revision, origin, reason string }{
		{"unknown", "unknown", decisionProtected},
		{"confirmed", "manual", decisionManual},
		{"later", "unknown", decisionManual},
	} {
		p := event(step.revision, "已有标题")
		p.TitleOrigin = step.origin
		call[accepted](t, e, "event.submit", p)
		result := revisionRecord(t, e, step.revision)
		if result.Reason != step.reason || result.Title != p.CurrentTitle || len(result.Attempts) != 0 {
			t.Fatalf("protection: %+v", result)
		}
	}
	if old := revisionRecord(t, e, "old"); old.Reason != "" || old.Status != "kept" {
		t.Fatalf("historical reason was inferred from the current lock: %+v", old)
	}
	page := call[recordList](t, e, "records.list", listRequest{Filter: "attention"})
	if page.Counts.Total != 4 || page.Counts.Attention != 3 || len(page.Items) != 3 {
		t.Fatalf("legacy vs current protection counts: %+v", page.Counts)
	}
}

func TestTitleValidationFailureRetriesNextNewTurnAfterRestart(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir, nil)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	var calls atomic.Int32
	configure := func(e *Engine) {
		e.now = func() time.Time { return time.UnixMilli(clock.Load()) }
		e.generate = func(_ context.Context, req attemptRequest, start func() error) modelResult {
			if err := start(); err != nil {
				t.Error(err)
				return modelResult{status: "failed"}
			}
			switch calls.Add(1) {
			case 2, 3:
				return modelResult{status: "completed", decision: decision{
					Action: "rename", Title: "⚙️ 回滚｜" + strings.Repeat("改", 16),
				}}
			default:
				return modelResult{status: "completed", decision: decision{Action: "keep", Reason: "private-fixture"}}
			}
		}
	}
	configure(e)
	p := event("baseline", goodTitle)
	p.TitleOrigin = "auto"
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, "baseline").Reason == decisionModelKeep })
	clock.Add(recheckCooldown.Milliseconds())
	p.Revision = "validation-failed"
	p.Turns = append(p.Turns, turn{"user", "继续"}, turn{"assistant", "收到"})
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "failed" })
	failure := revisionRecord(t, e, p.Revision)
	if len(failure.Attempts) != 2 || failure.Failure == nil || failure.Failure.Code != failureTooLong {
		t.Fatalf("validation: %+v", failure)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = engineAt(t, dir, nil)
	configure(e)
	p.Revision = "duplicate"
	call[accepted](t, e, "event.submit", p)
	if r := revisionRecord(t, e, p.Revision); r.Reason != decisionDuplicate || len(r.Attempts) != 0 {
		t.Fatalf("duplicate: %+v", r)
	}
	p.Revision = "next"
	p.SubstantiveActivity = true
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Reason == decisionModelKeep })
	r := revisionRecord(t, e, p.Revision)
	stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if len(r.Attempts) != 1 || calls.Load() != 4 || stats.ModelKept != 2 {
		t.Fatalf("retry did not require exactly one new review: %+v %+v, calls=%d", r, stats, calls.Load())
	}
}

func TestEmptyTitleAndNoContentHaveDifferentDecisionReasons(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		return modelResult{status: "completed", decision: decision{Action: "keep"}}
	}
	p := event("no-content", "")
	p.Turns = []turn{}
	call[accepted](t, e, "event.submit", p)
	if got := revisionRecord(t, e, p.Revision); got.Reason != decisionNoContent || len(got.Attempts) != 0 {
		t.Fatalf("no content: %+v", got)
	}
	p = event("title-not-generated", "")
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "kept" })
	if got := revisionRecord(t, e, p.Revision); got.Reason != decisionEmpty || len(got.Attempts) != 1 {
		t.Fatalf("empty decision: %+v", got)
	}
}

func TestRecordsCountAndSearchAllHistoryWithoutRepeatingPages(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	now := e.now().UnixMilli()
	for i := range 67 {
		id := fmt.Sprintf("record-%d", i)
		status, sync, reason := "kept", "", decisionModelKeep
		switch i {
		case 0:
			status, sync = "failed", "failed"
		case 1:
			reason = decisionManual
		case 2:
			reason = decisionProtected
		case 3:
			sync = "protected"
		case 4:
			status = "failed"
		}
		if status == "failed" {
			reason = ""
		}
		if _, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('claude',?,'r','样例标题',0)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.Exec(`INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,sync_status,
			decision_reason,created,updated) VALUES(?,'claude',?,'r','','样例标题',?,'','',?,?,?,?)`,
			id, id, status, sync, reason, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.Exec(`INSERT INTO attempts(event_id,agent,provider,model,actual_model,source,requested_tier,status,started)
		VALUES('record-0','claude','claude','request','old-resolved-model','fixture','default','completed',?)`, now); err != nil {
		t.Fatal(err)
	}
	first := call[recordList](t, e, "records.list", listRequest{})
	wantCounts := recordCounts{Total: 67, Attention: 5, Failed: 2, SyncFailed: 1, Protected: 3}
	if first.Counts != wantCounts || len(first.Items) != 50 || first.Next == nil {
		t.Fatalf("counts or first page: %+v", first.Counts)
	}
	seen := map[string]bool{}
	var next *recordCursor
	for {
		page := call[recordList](t, e, "records.list", listRequest{Filter: "attention", Limit: 2, Before: next})
		if page.Counts != wantCounts {
			t.Fatalf("inconsistent pagination count: %+v", page.Counts)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("repeated %s", item.ID)
			}
			seen[item.ID] = true
		}
		next = page.Next
		if next == nil {
			break
		}
	}
	if len(seen) != wantCounts.Attention {
		t.Fatalf("missing records: %v", seen)
	}
	found := call[recordList](t, e, "records.list", listRequest{Filter: "attention", Query: "Old-Resolved-Model"})
	if len(found.Items) != 1 || found.Items[0].ID != "record-0" || len(found.Items[0].Attempts) != 1 || found.Counts.Attention != 1 {
		t.Fatalf("search across all history: %+v", found)
	}
}
