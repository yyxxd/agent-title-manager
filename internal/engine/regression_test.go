package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPromptUsesExplicitGoalAndOnlySafeProjectLeaf(t *testing.T) {
	p := event("r", "")
	p.Project = `\\server\share\private\Example Repo\`
	for _, goal := range []string{"", strings.Repeat("界", 801)} {
		p.OriginalGoal = goal
		data, err := makePrompt(p)
		var prompt map[string]json.RawMessage
		if err != nil || json.Unmarshal(data, &prompt) != nil || string(prompt["project_hint"]) != `"Example Repo"` ||
			strings.Contains(string(data), "private") || strings.Contains(string(data), "server") {
			t.Fatalf("%s %v", data, err)
		}
		value, ok := prompt["original_goal"]
		if goal == "" && ok {
			t.Fatal("近期首条被误作原始目标")
		}
		if goal != "" && string(value) != `"`+strings.Repeat("界", 800)+`"` {
			t.Fatal("原始目标截断错误")
		}
	}
	for _, tc := range []struct{ path, hint string }{
		{`D:\team\Shore`, "Shore"},
		{`D:\`, ""},
		{`\\server\share`, ""},
		{`\\?\UNC\server\share\Shore`, "Shore"},
		{`C:\Users\TestName`, ""},
		{"/home/TestName", ""},
		{"/private/safe-repo", "safe-repo"},
		{"/private/folder@identity", ""},
	} {
		if hint, _ := inspectProject(tc.path); hint != tc.hint {
			t.Errorf("%q -> %q", tc.path, hint)
		}
	}
}

func TestConfirmationNeedsCurrentManagedTitleAndIntactPrefix(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	var calls atomic.Int32
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(usageReply))
	})
	p := event("baseline", "")
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", p.Revision})
	if c.Candidate == nil || c.Status != "claimed" {
		t.Fatal(c)
	}
	if !call[accepted](t, e, "title.ack", ackRequest{"codex", p.SessionID, c.Candidate.ID, goodTitle}).Accepted {
		t.Fatal("基线标题未应用")
	}
	if c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, goodTitle, p.Revision}); c.Status != "applied" {
		t.Fatal(c)
	}
	p.CurrentTitle, p.Revision, p.ConfirmationOnly = goodTitle, "confirm", true
	p.Turns = append(p.Turns, turn{"user", "好的。"}, turn{"assistant", "收到"})
	call[accepted](t, e, "event.submit", p)
	if c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, goodTitle, p.Revision}); c.Status != "kept" || c.Candidate != nil || calls.Load() != 1 {
		t.Fatal(c, calls.Load())
	}
	if got := latest(t, e); got.Reason != decisionConfirmation || len(got.Attempts) != 0 {
		t.Fatalf("pure confirmation: %+v", got)
	}
	p.Revision = "changed-baseline"
	p.Turns[0].Text = "此前用户目标被编辑"
	p.Turns = append(p.Turns, turn{"user", "好的"})
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "kept" && calls.Load() == 2 })
	if got := latest(t, e); got.Reason != decisionSame || len(got.Attempts) != 1 {
		t.Fatalf("same title after model call: %+v", got)
	}
}

func TestProjectDuplicatesIncludeReservedCandidates(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(usageReply)) })
	for i, tc := range []struct{ project, status string }{
		{`D:\one\repo`, "candidate"},
		{`d:/ONE/repo/`, "kept"},
		{`D:\two\repo`, "candidate"},
	} {
		p := event("r", "")
		p.SessionID, p.Project = fmt.Sprintf("session-%d", i), tc.project
		call[accepted](t, e, "event.submit", p)
		eventually(t, func() bool { return latest(t, e).SessionID == p.SessionID && latest(t, e).Status == tc.status })
		if got := latest(t, e); tc.status == "kept" && (got.Reason != decisionCollision || len(got.Attempts) != 1) {
			t.Fatalf("title collision: %+v", got)
		}
	}
	if s := call[dailyStats](t, e, "stats.daily", dateRequest{}); s.Attempts != 3 || s.Kept != 1 {
		t.Fatal(s)
	}
}

func TestQueuedAndRecoveredDisabledJobsNeverCallModel(t *testing.T) {
	dir := t.TempDir()
	var enabled atomic.Bool
	enabled.Store(true)
	e, err := Open(dir, nil, func(_ context.Context, agent string) bool { return agent == "codex" || enabled.Load() })
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	started, release := make(chan struct{}, 1), make(chan struct{})
	var activity atomic.Int32
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		if activity.Add(1) == 1 {
			started <- struct{}{}
			<-release
		}
		_, _ = w.Write([]byte(usageReply))
	})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	call[accepted](t, e, "event.submit", event("a", ""))
	select {
	case <-started:
	case <-time.After(6 * time.Second):
		t.Fatal("A 未开始")
	}
	b := event("b", "")
	b.Agent = "pi"
	call[accepted](t, e, "event.submit", b)
	if c := call[claimResponse](t, e, "title.claim", claimRequest{"pi", b.SessionID, "", "b"}); c.Status != "queued" {
		t.Fatal(c)
	}
	enabled.Store(false)
	close(release)
	eventually(t, func() bool {
		return call[claimResponse](t, e, "title.claim", claimRequest{"pi", b.SessionID, "", "b"}).Status == "disabled"
	})
	if activity.Load() != 1 {
		t.Fatal(activity.Load())
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	seedRestart(t, dir)
	next, err := Open(dir, nil, func(_ context.Context, agent string) bool { return agent != "pi" })
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	eventually(t, func() bool {
		return call[claimResponse](t, next, "title.claim", claimRequest{"pi", "queued-session", "", "queued-r"}).Status == "disabled"
	})
	if activity.Load() != 1 {
		t.Fatal(activity.Load())
	}
}

func TestDisabledBetweenPrimaryAndFallback(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	e, err := Open(t.TempDir(), nil, func(context.Context, string) bool { return on.Load() })
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.generate = func(_ context.Context, r attemptRequest, start func() error) modelResult {
		if r.provider != "codex" {
			t.Error("停用后发起了备用请求")
		}
		if err := start(); err != nil {
			return modelResult{status: "interrupted"}
		}
		on.Store(false)
		return modelResult{status: "failed", confirmed: true}
	}
	call[Settings](t, e, "settings.save", map[string]any{"fallbackEnabled": true, "apiBaseUrl": "https://example.invalid", "apiModel": "test"})
	call[accepted](t, e, "event.submit", event("r", ""))
	eventually(t, func() bool { return latest(t, e).Status == "disabled" })
	if len(latest(t, e).Attempts) != 1 {
		t.Fatal(latest(t, e))
	}
}

func TestSafeSyncSkipHasNoRetry(t *testing.T) {
	e := engineAt(t, t.TempDir(), func(context.Context, string, string, string) error {
		return fmt.Errorf("现场原标题变化: %w", ErrSyncSkipped)
	})
	empty := ""
	p := submission{Agent: "pi", SessionID: "native", Revision: "r", CurrentTitle: "已有原生名", OrcaID: "raw",
		OrcaTitle: &empty, NativeOnly: true, Turns: []turn{}}
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).SyncStatus == "skipped" })
	if call[accepted](t, e, "sync.retry", idRequest{latest(t, e).ID}).Accepted {
		t.Fatal("安全跳过进入重试")
	}
	if s := call[dailyStats](t, e, "stats.daily", dateRequest{}); s.Attempts != 0 || s.InputTokens != nil {
		t.Fatal(s)
	}
}

func TestNativeIdentityIsRecheckedOnAdoptAndRetry(t *testing.T) {
	var checks atomic.Int32
	e := engineAt(t, t.TempDir(), func(ctx context.Context, raw, old, next string) error {
		got, ok := SyncTargetFromContext(ctx)
		want := SyncTarget{"pi", "native", "verified-handle", "已观察原生名"}
		if !ok || got != want || raw != "raw" || old != "原 Orca 标题" || next != want.ExpectedTitle {
			t.Errorf("原生写前校验缺少绑定: %+v %v", got, ok)
		}
		if checks.Add(1) == 1 {
			return fmt.Errorf("暂未读到原生标题")
		}
		return ErrSyncStale
	})
	old := "原 Orca 标题"
	p := submission{Agent: "pi", SessionID: "native", Revision: "r", CurrentTitle: "已观察原生名",
		OrcaID: "raw", OrcaTitle: &old, OrcaNativeHandle: "verified-handle", NativeOnly: true, Turns: []turn{}}
	call[accepted](t, e, "event.submit", p)
	id := latest(t, e).ID
	if !call[accepted](t, e, "sync.adopt", idRequest{id}).Accepted {
		t.Fatal("采用未入队")
	}
	eventually(t, func() bool { return latest(t, e).SyncStatus == "failed" })
	if !call[accepted](t, e, "sync.retry", idRequest{id}).Accepted {
		t.Fatal("仍缺证据的同步无法重试")
	}
	eventually(t, func() bool { return latest(t, e).SyncStatus == "stale" })
	if call[accepted](t, e, "sync.retry", idRequest{id}).Accepted || call[accepted](t, e, "sync.adopt", idRequest{id}).Accepted ||
		checks.Load() != 2 || call[dailyStats](t, e, "stats.daily", dateRequest{}).Attempts != 0 {
		t.Fatal("过期记录可再次执行，或免模型路径产生用量")
	}
}

func TestMigrateExistingEngineDatabase(t *testing.T) {
	dir := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`INSERT INTO attempts(agent,provider,model,source,requested_tier,status,started,input_tokens,output_tokens)
		VALUES('test','api','v1','openai_compatible','default','completed',?,50,8)`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir, nil)
	s := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if s.Attempts != 1 || !hasCount(s.InputTokens, 50) || s.CachedTokens != nil || s.ByModel[0].Model != "v1" {
		t.Fatal(s)
	}
}
