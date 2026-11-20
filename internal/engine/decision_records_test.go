package engine

import (
	"context"
	"database/sql"
	"encoding/json"
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
	if page.Counts.Total != 4 || page.Counts.Attention != 0 || len(page.Items) != 0 {
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
	eventually(t, func() bool { return revisionRecord(t, e, "baseline").Reason == decisionKeepUnknown })
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
	p.ConfirmationOnly = true
	call[accepted](t, e, "event.submit", p)
	if r := revisionRecord(t, e, p.Revision); r.Reason != decisionDuplicate || len(r.Attempts) != 0 {
		t.Fatalf("duplicate: %+v", r)
	}
	p.Revision = "next"
	p.ConfirmationOnly = false
	p.SubstantiveActivity = true
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Reason == decisionKeepUnknown })
	r := revisionRecord(t, e, p.Revision)
	stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if len(r.Attempts) != 1 || calls.Load() != 4 || stats.ModelKept != 2 {
		t.Fatalf("retry did not require exactly one new review: %+v %+v, calls=%d", r, stats, calls.Load())
	}
}

func TestModelKeepReasonsUseFixedCodesAndCountAllKeeps(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	const private = "fixture-private-key-and-transcript"
	cases := []struct{ reason, want string }{
		{"延续原标题目标。", decisionKeepGoal},
		{"只是已有工作的步骤或进展", decisionKeepProgress},
		{"", decisionKeepUnknown},
		{"没有把握判断", decisionKeepUnknown},
		{"延续原标题目标。 " + private, decisionKeepUnknown},
	}
	var calls atomic.Int32
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		tc := cases[calls.Add(1)-1]
		return modelResult{status: "completed", decision: decision{Action: "keep", Reason: tc.reason}}
	}
	for i, tc := range cases {
		p := event(fmt.Sprintf("keep-%d", i), goodTitle)
		p.SessionID, p.TitleOrigin = p.Revision, "auto"
		p.Turns = append(p.Turns, turn{"user", private})
		call[accepted](t, e, "event.submit", p)
		eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "kept" })
		got := revisionRecord(t, e, p.Revision)
		if got.Reason != tc.want || len(got.Attempts) != 1 {
			t.Fatalf("unexpected keep reason or invocation count: %+v", got)
		}
		wire, err := json.Marshal(got)
		if err != nil || strings.Contains(string(wire), private) {
			t.Fatalf("record exposes private input: err=%v", err)
		}
		var reason string
		var payload []byte
		if err := e.db.QueryRow("SELECT decision_reason,payload FROM events WHERE id=?", got.ID).Scan(&reason, &payload); err != nil || reason != tc.want || payload != nil {
			t.Fatalf("expected only a fixed stored reason: %v", err)
		}
	}
	if _, err := e.db.Exec("UPDATE events SET decision_reason=? WHERE revision='keep-0'", decisionModelKeep); err != nil {
		t.Fatal(err)
	}
	if got := call[dailyStats](t, e, "stats.daily", dateRequest{}); got.ModelKept != int64(len(cases)) ||
		got.Attempts != int64(len(cases)) || calls.Load() != int32(len(cases)) {
		t.Fatalf("mixed generations of keep reasons: %+v", got)
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

func TestConfirmationAfterValidationFailureWaitsForSubstantiveTurn(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	var clock atomic.Int64
	var calls atomic.Int32
	clock.Store(time.Now().UnixMilli())
	e.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		switch calls.Add(1) {
		case 2, 3:
			return modelResult{status: "completed", decision: decision{Action: "rename", Title: "🧩 项目｜" + strings.Repeat("长", 24)}}
		default:
			return modelResult{status: "completed", decision: decision{Action: "rename", Title: goodTitle}}
		}
	}
	p := event("initial", "")
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, "", p.Revision}).Candidate
	if c == nil || !call[accepted](t, e, "title.ack", ackRequest{p.Agent, p.SessionID, c.ID, c.Title}).Accepted {
		t.Fatal("could not establish managed title")
	}
	p.CurrentTitle = c.Title
	clock.Add(recheckCooldown.Milliseconds())
	p.Revision = "invalid"
	p.Turns = append(p.Turns, turn{"user", "实现新需求"}, turn{"assistant", "整理了实现步骤"})
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "failed" })
	for i, text := range []string{"好的", "收到", "可以", "谢谢", "嗯嗯"} {
		p.Revision, p.ConfirmationOnly = fmt.Sprintf("confirm-%d", i), true
		p.Turns = append(p.Turns, turn{"user", text}, turn{"assistant", "收到"})
		p.Turns = p.Turns[max(0, len(p.Turns)-8):]
		call[accepted](t, e, "event.submit", p)
		got := revisionRecord(t, e, p.Revision)
		if got.Reason != decisionConfirmation || len(got.Attempts) != 0 || calls.Load() != 3 {
			t.Fatalf("confirmation should preserve a pending review: %+v, calls=%d", got, calls.Load())
		}
	}
	p.Revision, p.ConfirmationOnly, p.SubstantiveActivity = "tool-progress", false, true
	p.Turns = append(p.Turns[2:], turn{"user", "继续"}, turn{"assistant", "工具已完成校验"})
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Reason == decisionSame })
	if got := revisionRecord(t, e, p.Revision); len(got.Attempts) != 1 || calls.Load() != 4 {
		t.Fatalf("tool progress should trigger one immediate review: %+v, calls=%d", got, calls.Load())
	}
}

func TestRecordsCountAndSearchAllHistoryWithoutRepeatingPages(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	now := e.now().UnixMilli()
	for i := range 67 {
		id := fmt.Sprintf("record-%d", i)
		status, sync, reason := "kept", "", decisionModelKeep
		orcaID, sessionOrcaID := "", ""
		var orcaBase *string
		switch i {
		case 0:
			status, sync = "failed", "failed"
		case 1:
			reason = decisionManual
		case 2:
			reason = decisionProtected
		case 3:
			sync = "protected"
			reason = decisionManual
			orcaID, sessionOrcaID = "orca-current", "orca-current"
			old := "Orca 原标题"
			orcaBase = &old
		case 4:
			status = "failed"
		case 5:
			sync = "protected"
			orcaID, sessionOrcaID = "orca-old", "orca-new"
			old := "Orca 旧绑定标题"
			orcaBase = &old
		}
		if status == "failed" {
			reason = ""
		}
		if _, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id) VALUES('claude',?,'r','样例标题',0,?)`, id, sessionOrcaID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.Exec(`INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_base,orca_origin,sync_status,
			decision_reason,created,updated) VALUES(?,'claude',?,'r','','样例标题',?,?,?,'',?,?,?,?)`,
			id, id, status, orcaID, orcaBase, sync, reason, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.Exec(`INSERT INTO attempts(event_id,agent,provider,model,actual_model,source,requested_tier,status,started)
		VALUES('record-0','claude','claude','request','old-resolved-model','fixture','default','completed',?)`, now); err != nil {
		t.Fatal(err)
	}
	first := call[recordList](t, e, "records.list", listRequest{})
	wantCounts := recordCounts{Total: 67, Attention: 3, Failed: 2, SyncFailed: 1, Protected: 1}
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
			if item.ID == "record-3" && (!item.CanAdopt || item.Reason != decisionManual) {
				t.Fatalf("native lock hid actionable Orca protection: %+v", item)
			}
		}
		next = page.Next
		if next == nil {
			break
		}
	}
	if len(seen) != wantCounts.Attention {
		t.Fatalf("missing records: %v", seen)
	}
	if !seen["record-0"] || !seen["record-3"] || !seen["record-4"] {
		t.Fatalf("wrong pending records: %v", seen)
	}
	found := call[recordList](t, e, "records.list", listRequest{Filter: "attention", Query: "Old-Resolved-Model"})
	if len(found.Items) != 1 || found.Items[0].ID != "record-0" || len(found.Items[0].Attempts) != 1 || found.Counts.Attention != 1 {
		t.Fatalf("search across all history: %+v", found)
	}
}
