package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const goodTitle = "🧩 构建｜修复路径"
const usageReply = `{"choices":[{"message":{"role":"assistant","content":"{\"action\":\"rename\",\"title\":\"🧩 构建｜修复路径\",\"reason\":\"goal\"}"}}],"usage":{"prompt_tokens":50,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":12}},"service_tier":"default"}`

func call[T any](t *testing.T, e *Engine, method string, p any) T {
	t.Helper()
	params, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Handle(context.Background(), method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return v.(T)
}

func engineAt(t *testing.T, dir string, syncer func(context.Context, string, string, string) error) *Engine {
	t.Helper()
	e, err := Open(dir, syncer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
	return e
}

func event(revision, title string) submission {
	return submission{Agent: "codex", SessionID: "session-test", Revision: revision, CurrentTitle: title,
		Turns: []turn{{"user", "修复构建路径"}, {"assistant", "先检查配置"}}}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("状态在 6 秒内未达到预期")
}

func latest(t *testing.T, e *Engine) record {
	t.Helper()
	result := call[recordList](t, e, "records.list", listRequest{})
	if len(result.Items) == 0 {
		t.Fatal("未找到记录")
	}
	return result.Items[0]
}

func httpProvider(t *testing.T, e *Engine, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	call[Settings](t, e, "settings.save", map[string]any{"provider": "api", "apiBaseUrl": server.URL + "/v1", "apiModel": "test-model"})
}

func TestConcurrentSubmissionAndReadback(t *testing.T) {
	var requests, writes atomic.Int64
	e := engineAt(t, t.TempDir(), func(_ context.Context, raw, expected, next string) error {
		if raw != "raw-1" || expected != "" || next != goodTitle {
			t.Errorf("意外同步: %q %q %q", raw, expected, next)
		}
		if writes.Add(1) == 1 {
			return errors.New("模拟写入失败")
		}
		return nil
	})
	httpProvider(t, e, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
			t.Error(r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(usageReply))
	})
	p := event("r1", "")
	empty := ""
	p.OrcaID, p.OrcaTitle = "raw-1", &empty
	submitConcurrently(t, e, p)
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", "r1"}).Candidate
	c2 := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", "r1"}).Candidate
	if c == nil || c2 == nil || c.ID != c2.ID || requests.Load() != 1 {
		t.Fatal(c, c2, requests.Load())
	}
	if r := call[accepted](t, e, "title.ack", ackRequest{"codex", p.SessionID, c.ID, "还没写入"}); r.Accepted {
		t.Fatal("误计改名")
	}
	if s := call[dailyStats](t, e, "stats.daily", dateRequest{}); s.Renames != 0 || writes.Load() != 0 {
		t.Fatal(s, writes.Load())
	}
	for range 2 {
		if !call[accepted](t, e, "title.ack", ackRequest{"codex", p.SessionID, c.ID, goodTitle}).Accepted {
			t.Fatal("确认失败")
		}
	}
	eventually(t, func() bool { return latest(t, e).SyncStatus == "failed" })
	if r := latest(t, e); r.Status != "applied" || r.Failure != nil {
		t.Fatal(r)
	}
	if !call[accepted](t, e, "sync.retry", idRequest{c.ID}).Accepted {
		t.Fatal("重试未入队")
	}
	eventually(t, func() bool { return latest(t, e).SyncStatus == "synced" })
	s := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if s.Attempts != 1 || s.Renames != 1 || !hasCount(s.CachedTokens, 12) || !hasCount(s.InputTokens, 50) || !hasCount(s.OutputTokens, 8) ||
		s.UnknownUsage != 0 || requests.Load() != 1 || writes.Load() != 2 {
		t.Fatal(s, requests.Load(), writes.Load())
	}
}

func hasCount(count *int64, expected int64) bool {
	return count != nil && *count == expected
}

func submitConcurrently(t *testing.T, e *Engine, p submission) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			v, err := e.Handle(context.Background(), "event.submit", raw)
			if err != nil || v != (accepted{Accepted: true}) {
				t.Errorf("submit %v %v", v, err)
			}
		})
	}
	wg.Wait()
}

func TestCandidateIsStaleWhenManualTitleArrivesDuringModelCall(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	started, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	defer release()
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-released
		_, _ = w.Write([]byte(usageReply))
	})
	p := event("r1", "")
	call[accepted](t, e, "event.submit", p)
	select {
	case <-started:
	case <-time.After(6 * time.Second):
		t.Fatal("未发起模型请求")
	}
	if call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", "r1"}).Candidate != nil {
		t.Fatal("请求完成前已经发放候选")
	}
	call[accepted](t, e, "event.submit", event("r2", "现有手动名"))
	release()
	eventually(t, func() bool {
		return containsStatus(call[recordList](t, e, "records.list", listRequest{}), "r1", "stale")
	})
}

func TestSupersededCandidateAndManualTitle(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(usageReply)) })
	p := event("r1", "")
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", "r1"}).Candidate
	next := event("r2", "用户指定名字")
	call[accepted](t, e, "event.submit", next)
	if call[accepted](t, e, "title.ack", ackRequest{"codex", p.SessionID, c.ID, goodTitle}).Accepted {
		t.Fatal("接受了旧候选")
	}
	call[accepted](t, e, "event.submit", event("r3", "用户指定名字"))
	if !containsStatus(call[recordList](t, e, "records.list", listRequest{}), "r3", "kept") {
		t.Fatal("手动名未保护")
	}
	stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if stats.Attempts != 1 || stats.Renames != 0 {
		t.Fatal(stats)
	}
}

func containsStatus(rs recordList, revision, status string) bool {
	for _, r := range rs.Items {
		if r.Revision == revision && r.Status == status {
			return true
		}
	}
	return false
}

func TestAckAfterLaterRevisionAlreadyObservedCandidate(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(usageReply)) })
	p := event("r1", "")
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{"codex", p.SessionID, "", "r1"}).Candidate
	call[accepted](t, e, "event.submit", event("r2", goodTitle))
	if !call[accepted](t, e, "title.ack", ackRequest{"codex", p.SessionID, c.ID, goodTitle}).Accepted {
		t.Fatal("已被后续版本读回的候选未确认")
	}
	if s := call[dailyStats](t, e, "stats.daily", dateRequest{}); s.Renames != 1 {
		t.Fatal(s)
	}
}

func TestNativeOnlyProtectsAndAdoptsOrcaTitle(t *testing.T) {
	calls := make(chan [3]string, 3)
	e := engineAt(t, t.TempDir(), func(_ context.Context, id, expected, next string) error {
		calls <- [3]string{id, expected, next}
		return nil
	})
	manual := "Orca 的现有标题"
	p := submission{Agent: "claude", SessionID: "n1", Revision: "native-r1", CurrentTitle: "已有原生名", OrcaID: "raw-1", OrcaTitle: &manual, NativeOnly: true, Turns: []turn{}}
	call[accepted](t, e, "event.submit", p)
	r := latest(t, e)
	if r.Status != "native" || r.SyncStatus != "protected" || len(r.Attempts) != 0 {
		t.Fatal(r)
	}
	if !call[accepted](t, e, "sync.adopt", idRequest{r.ID}).Accepted {
		t.Fatal("采用失败")
	}
	eventually(t, func() bool { return latest(t, e).SyncStatus == "synced" })
	if got := <-calls; got != ([3]string{"raw-1", manual, p.CurrentTitle}) {
		t.Fatal(got)
	}
	managed := p.CurrentTitle
	p.Revision, p.CurrentTitle, p.OrcaTitle = "native-r2", "后续原生名", &managed
	p.OrcaOrigin = "manual" // Orca also labels our verified writes as user-originated.
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).SyncStatus == "synced" })
	if got := <-calls; got != ([3]string{"raw-1", managed, p.CurrentTitle}) {
		t.Fatal(got)
	}
	if s := call[dailyStats](t, e, "stats.daily", dateRequest{}); s.Attempts != 0 || s.Renames != 0 || s.Kept != 0 {
		t.Fatal(s)
	}
}

func TestRestartRecoversQueuedAndQuarantinesIndeterminateAttempts(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir, nil)
	if dup, err := Open(dir, nil); err == nil {
		dup.Close()
		t.Fatal("同目录启动了两个核心")
	}
	var calls atomic.Int64
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(usageReply))
	})
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	seedRestart(t, dir)
	next := engineAt(t, dir, nil)
	eventually(t, func() bool {
		records := call[recordList](t, next, "records.list", listRequest{})
		return containsStatus(records, "queued-r", "candidate") && containsStatus(records, "running-r", "interrupted")
	})
	call[accepted](t, next, "event.submit", submission{Agent: "pi", SessionID: "running-session", Revision: "running-r", Turns: []turn{}})
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if s := call[dailyStats](t, next, "stats.daily", dateRequest{}); s.Attempts != 2 || s.UnknownUsage != 1 {
		t.Fatal(s)
	}
}

func seedRestart(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	prompt, err := makePrompt(event("r", ""))
	if err != nil {
		t.Fatal(err)
	}
	stmts := []struct {
		q      string
		params []any
	}{
		{`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES ('pi','queued-session','queued-r','',0),('pi','running-session','running-r','',0)`, nil},
		{`INSERT INTO events(id,agent,session_id,revision,base,title,status,payload,orca_id,orca_origin,created,updated)
			VALUES ('q','pi','queued-session','queued-r','','','queued',?,'','',?,?),
			('r','pi','running-session','running-r','','','running',NULL,'','',?,?)`, []any{prompt, now, now, now, now}},
		{`INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started)
			VALUES ('r','pi','codex','test-model','codex_subscription','priority','running',?)`, []any{now}},
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt.q, stmt.params...); err != nil {
			t.Fatal(err)
		}
	}
}
