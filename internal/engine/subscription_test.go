package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionCatalogs(t *testing.T) {
	mode := installSubscriptionFixtures(t)
	e := engineAt(t, t.TempDir(), nil)
	codex := call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"codex"})
	if len(codex.Items) != 2 || codex.Items[0].ID != "fixture-codex" || codex.Items[0].DefaultEffort != "high" ||
		len(codex.Items[1].Efforts) != 0 || codex.Items[1].DefaultEffort != "" {
		t.Fatal(codex)
	}
	claude := call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"claude"})
	if len(claude.Items) != 2 || claude.Items[0].ResolvedModel != "fixture-resolved" ||
		len(claude.Items[0].Efforts) != 1 || len(claude.Items[1].Efforts) != 0 || claude.Notice != "" {
		t.Fatal(claude)
	}
	mode("empty")
	if empty, err := e.subscriptionModels(t.Context(), "codex"); err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal(empty, err)
	}
	mode("old")
	for _, provider := range []string{"codex", "claude"} {
		if _, err := e.subscriptionModels(t.Context(), provider); !errors.Is(err, errCatalogProtocol) {
			t.Fatal(provider, err)
		}
	}
	mode("custom-catalog")
	if _, err := e.subscriptionModels(t.Context(), "codex"); err == nil || !strings.Contains(err.Error(), "自定义") {
		t.Fatal(err)
	}
	mode("timeout")
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := e.subscriptionModels(ctx, "claude"); !errors.Is(err, errCatalogTimeout) {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, provider := range []string{"codex", "claude"} {
		if _, err := e.subscriptionModels(t.Context(), provider); err == nil || !strings.Contains(err.Error(), "找不到") {
			t.Fatal(provider, err)
		}
	}
}

func TestSubscriptionCatalogRequiresLogin(t *testing.T) {
	mode := installSubscriptionFixtures(t)
	e := engineAt(t, t.TempDir(), nil)
	saved := call[Settings](t, e, "settings.save", map[string]string{
		"provider": "claude", "claudeModel": "fixture-claude", "claudeEffort": "max",
	})
	for _, fixture := range []struct{ provider, auth string }{
		{"codex", "noauth"}, {"claude", "noauth"}, {"claude", "injected-oauth"},
	} {
		mode(fixture.auth)
		catalog := call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{fixture.provider})
		if !catalog.AuthRequired || catalog.Items == nil || len(catalog.Items) != 0 || !strings.Contains(catalog.Notice, "登录") {
			t.Fatal(fixture, catalog)
		}
		args, _ := json.Marshal(map[string]string{"provider": fixture.provider})
		assertRPCError(t, e, "providers.test", args, "登录")
	}
	binary, err := claudeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(binary), "unauthenticated-catalog.started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("未登录时启动了目录子进程", err)
	}
	if data := call[recordList](t, e, "records.list", listRequest{}); len(data.TestAttempts) != 0 {
		t.Fatal(data.TestAttempts)
	}
	if settings := call[Settings](t, e, "settings.get", nil); settings != saved {
		t.Fatal(settings, saved)
	}
	mode("")
	catalog := call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"claude"})
	if catalog.AuthRequired || len(catalog.Items) != 2 || catalog.Items[0].ID != saved.ClaudeModel ||
		len(catalog.Items[0].Efforts) != 1 || catalog.Items[0].Efforts[0] != saved.ClaudeEffort {
		t.Fatal(catalog)
	}
}

func TestSubscriptionDraftValidation(t *testing.T) {
	mode := installSubscriptionFixtures(t)
	e := engineAt(t, t.TempDir(), nil)
	saved := call[Settings](t, e, "settings.get", nil)
	if saved.CodexModel != "" || saved.CodexEffort != "" {
		t.Fatal(saved)
	}
	codex := json.RawMessage(`{"provider":"codex","codexModel":"fixture-codex","codexEffort":"high","fast":true}`)
	result, err := e.Handle(t.Context(), "providers.test", codex)
	if err != nil || result.(testResult).Model != "fixture-codex" {
		t.Fatal(result, err)
	}
	if s := call[Settings](t, e, "settings.get", nil); s != saved {
		t.Fatal("测试修改了保存配置", s)
	}
	invalid := json.RawMessage(`{"provider":"codex","codexModel":"fixture-no-effort","codexEffort":"high"}`)
	assertRPCError(t, e, "settings.save", invalid, "不支持")
	assertRPCError(t, e, "providers.test", invalid, "不支持")
	choice := json.RawMessage(`{"provider":"codex","codexModel":"fixture-no-effort","codexEffort":""}`)
	if _, err := e.Handle(t.Context(), "settings.save", choice); err != nil {
		t.Fatal(err)
	}
	mode("empty")
	call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"codex"})
	assertRPCError(t, e, "providers.test", choice, "重新选择")
	if s := call[Settings](t, e, "settings.save", map[string]bool{"fast": false}); s.CodexModel != "fixture-no-effort" {
		t.Fatal("已保存模型丢失", s)
	}
	assertRPCError(t, e, "settings.save", codex, "重新选择")
}

func TestSubscriptionRefreshKeepsNewestSnapshot(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan subscriptionCatalog, 1)
	e.catalog = func(ctx context.Context, provider string) (subscriptionCatalog, error) {
		switch provider {
		case "claude":
			return subscriptionCatalog{Items: []subscriptionModel{{ID: "claude"}}}, nil
		case "codex":
			if ctx == e.ctx {
				close(started)
				<-release
				return subscriptionCatalog{Items: []subscriptionModel{{ID: "outdated"}}}, nil
			}
			return subscriptionCatalog{Items: []subscriptionModel{{ID: "current"}}}, nil
		default:
			return subscriptionCatalog{}, errCatalogProtocol
		}
	}
	go func() {
		value, _ := e.refreshSubscription(e.ctx, "codex")
		returned <- value
	}()
	<-started
	call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"claude"})
	call[subscriptionCatalog](t, e, "providers.subscriptionModels", subscriptionRequest{"codex"})
	close(release)
	if items := (<-returned).Items; len(items) != 1 || items[0].ID != "current" {
		t.Fatal(items)
	}
	if err := e.checkSubscription(t.Context(), Settings{Provider: "codex", CodexModel: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := e.checkSubscription(t.Context(), Settings{Provider: "claude", ClaudeModel: "claude"}); err != nil {
		t.Fatal(err)
	}
}

func assertRPCError(t *testing.T, e *Engine, method string, params json.RawMessage, want string) {
	t.Helper()
	if _, err := e.Handle(t.Context(), method, params); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s 要求 %q，收到 %v", method, want, err)
	}
}

func TestLegacySubscriptionSettingsMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5,
		`UPDATE settings SET codex_model='legacy-exact-model',fast=0,fallback=1 WHERE id=1`,
		`INSERT INTO attempts(provider,model,source,requested_tier,status,started,agent,input_tokens)
		 VALUES('codex','legacy-exact-model','codex_subscription','default','completed',1,'codex',3)`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir, nil)
	s := call[Settings](t, e, "settings.get", nil)
	if s.CodexModel != "legacy-exact-model" || s.CodexEffort != "low" || s.Fast || !s.FallbackEnabled || s.ClaudeModel != "" {
		t.Fatal(s)
	}
	history := call[recordList](t, e, "records.list", listRequest{}).TestAttempts
	if len(history) != 1 || history[0].Model != s.CodexModel || !hasCount(history[0].InputTokens, 3) {
		t.Fatal(history)
	}
}

func TestClaudeSubscriptionNamingAndReadback(t *testing.T) {
	mode := installSubscriptionFixtures(t)
	e := engineAt(t, t.TempDir(), nil)
	draft := json.RawMessage(`{"provider":"claude","claudeModel":"fixture-claude","claudeEffort":"max","fallbackEnabled":true}`)
	if reply, err := e.Handle(t.Context(), "providers.test", draft); err != nil || reply.(testResult).Model != "fixture-resolved" {
		t.Fatal(reply, err)
	}
	if _, err := e.Handle(t.Context(), "settings.save", draft); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude", "pi"} {
		p := event("valid", "")
		p.Agent = agent
		p.SessionID = agent + "-fixture"
		call[accepted](t, e, "event.submit", p)
		eventually(t, func() bool {
			r := latest(t, e)
			return r.SessionID == p.SessionID && r.Status == "candidate"
		})
		c := call[claimResponse](t, e, "title.claim", claimRequest{agent, p.SessionID, "", p.Revision}).Candidate
		if c == nil || c.Title != goodTitle || !call[accepted](t, e, "title.ack", ackRequest{agent, p.SessionID, c.ID, goodTitle}).Accepted {
			t.Fatal(agent, c)
		}
	}
	stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if len(stats.ByModel) != 1 || stats.ByModel[0].Model != "fixture-resolved" || stats.Renames != 3 ||
		stats.TestAttempts != 1 || !hasCount(stats.InputTokens, 56) || !hasCount(stats.CachedTokens, 8) {
		t.Fatal(stats)
	}
	mode("refused")
	p := event("refused", "")
	p.SessionID = "refusal-fixture"
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "failed" })
	r := latest(t, e)
	if r.Failure == nil || r.Failure.Code != failureRateLimited || len(r.Attempts) != 1 {
		t.Fatal(r)
	}
	a := r.Attempts[0]
	if a.Model != "fixture-claude" || a.ActualModel != "" || a.InputTokens != nil || a.CachedTokens != nil || a.Source != "claude_subscription" {
		t.Fatal(a)
	}
}

func TestClaudeSubscriptionPreflightAndTimeout(t *testing.T) {
	mode := installSubscriptionFixtures(t)
	e := engineAt(t, t.TempDir(), nil)
	s := Settings{ClaudeModel: "fixture-claude", ClaudeEffort: "max"}
	started := 0
	start := func() error { started++; return nil }
	for _, auth := range []string{"noauth", "injected-oauth"} {
		mode(auth)
		r := e.runClaude(t.Context(), s, "fixture", nil, start)
		if started != 0 || r.failure != failureClaudeLogin || r.confirmed {
			t.Fatal(auth, r, started)
		}
	}
	mode("ambiguous")
	r := e.runClaude(t.Context(), s, "fixture", nil, start)
	if started != 1 || r.status != "failed" || r.confirmed || r.usage.Input != nil {
		t.Fatal(r, started)
	}
	mode("timeout")
	ctx, cancel := context.WithTimeout(t.Context(), 750*time.Millisecond)
	defer cancel()
	r = e.runClaude(ctx, s, "fixture", nil, start)
	if started != 2 || r.status != "interrupted" || r.failure != failureTimeout || r.confirmed {
		t.Fatal(r, started)
	}
}

func TestClaudeStopsUnconfirmedRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := claudeOutput{cancel: cancel}
	_, _ = out.Write([]byte(`{"type":"system","subtype":"api_retry","error_status":429}` + "\n"))
	if ctx.Err() != nil {
		t.Fatal("已确认拒绝的重试被中止")
	}
	_, _ = out.Write([]byte(`{"type":"system","subtype":"api_`))
	_, _ = out.Write([]byte(`retry","error_status":null}` + "\n"))
	if ctx.Err() == nil || !out.unconfirmedRetry {
		t.Fatal("未中止结果无法确认的重试")
	}
}
