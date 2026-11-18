package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexJSONUsageAndFailure(t *testing.T) {
	stdout := []byte(`{"type":"thread.started","thread_id":"thread-test"}
{"type":"item.completed","item":{"type":"agent_message","text":"{\"action\":\"rename\",\"title\":\"🧩 构建｜修复路径\"}"}}
{"type":"turn.completed","usage":{"input_tokens":60,"cached_input_tokens":21,"output_tokens":11}}`)
	r := parseCodex(stdout, false, nil, nil)
	if r.status != "completed" || r.actualTier != "" || r.decision.Title != goodTitle || *r.usage.Cached != 21 {
		t.Fatal(r)
	}
	withTool := append([]byte(`{"type":"item.started","item":{"type":"command_execution"}}`+"\n"), stdout...)
	if r = parseCodex(withTool, false, nil, nil); r.status != "rejected_tool" || r.usage.Input == nil {
		t.Fatal(r)
	}
	confirmed := []byte(`{"type":"turn.failed","error":{"status":429,"code":"rate_limit_exceeded"}}`)
	if r = parseCodex(confirmed, false, errors.New("exit 1"), nil); !r.confirmed || r.failure != failureRateLimited {
		t.Fatal(r)
	}
	if r = parseCodex(confirmed, false, nil, context.DeadlineExceeded); r.confirmed || r.status != "interrupted" {
		t.Fatal(r)
	}
	if r = parseCodex(nil, false, errors.New("exit 1"), nil); r.confirmed {
		t.Fatal(r)
	}
}

func TestAPIMissingCacheAndInvalidPayload(t *testing.T) {
	r := parseAPI(protocolChat, strings.NewReader(`{"choices":[{"message":{"content":"invalid"}}],"usage":{"prompt_tokens":15,"completion_tokens":0}}`))
	if r.status != "invalid_response" || r.usage.Input == nil || *r.usage.Input != 15 || r.usage.Cached != nil ||
		r.usage.Output == nil || *r.usage.Output != 0 {
		t.Fatal(r)
	}
}

func TestCodexStopsAmbiguousReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := codexOutput{cancel: cancel}
	_, _ = out.Write([]byte(`{"type":"error","message":"Reconnecting... 1/5 (unexpected status 401 Unauthorized: fixture)"}` + "\n"))
	if ctx.Err() != nil {
		t.Fatal("明确拒绝不属于用量不明的中断")
	}
	_, _ = out.Write([]byte(`{"type":"error","message":"Reconnect`))
	_, _ = out.Write([]byte(`ing... 1/5 (stream disconnected)"}` + "\n"))
	if ctx.Err() == nil {
		t.Fatal("未阻止用量不明的重连")
	}
}

func TestFallbackDefaultAndConfirmedRejection(t *testing.T) {
	for _, tc := range []struct {
		label              string
		enabled, confirmed bool
		calls              int64
	}{{"default", false, true, 1}, {"ambiguous", true, false, 1}, {"confirmed", true, true, 2}} {
		t.Run(tc.label, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			e.generate = func(_ context.Context, r attemptRequest, start func() error) modelResult {
				if err := start(); err != nil {
					return modelResult{status: "interrupted"}
				}
				if r.provider == "codex" {
					return modelResult{status: "failed", confirmed: tc.confirmed}
				}
				return modelResult{status: "completed", decision: decision{Action: "keep"}}
			}
			call[Settings](t, e, "settings.save", map[string]any{
				"fallbackEnabled": tc.enabled, "apiBaseUrl": "https://example.invalid/v1", "apiModel": "demo",
			})
			call[accepted](t, e, "event.submit", event("r", ""))
			eventually(t, func() bool {
				status := latest(t, e).Status
				return status == "failed" || status == "kept"
			})
			r := latest(t, e)
			if int64(len(r.Attempts)) != tc.calls || r.Attempts[0].RequestedTier != "priority" ||
				r.Attempts[0].Source != "codex_subscription" || r.Attempts[0].ActualTier != "unknown" {
				t.Fatal(r)
			}
			if tc.calls == 2 && r.Attempts[1].Source != "openai_compatible" {
				t.Fatal(r)
			}
		})
	}
}

func TestProviderDoesNotForwardCredentialsToRedirect(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { redirected = true }))
	defer target.Close()
	e := engineAt(t, t.TempDir(), nil)
	httpProvider(t, e, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	params := []byte(`{"provider":"api"}`)
	_, err := e.Handle(context.Background(), "providers.test", params)
	if err == nil || !strings.Contains(err.Error(), "307") || redirected {
		t.Fatal(err, redirected)
	}
}

func TestLocalDayUsesAttemptStartAndSeparatesConfirmedWrites(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	local := time.FixedZone("test", 8*60*60)
	midnight := time.Date(2026, 1, 11, 0, 0, 0, 0, local)
	statsEngine := &Engine{db: e.db, now: func() time.Time { return midnight }}
	const useQuery = `INSERT INTO attempts(agent,provider,model,source,requested_tier,status,started,input_tokens,output_tokens,cached_tokens)
		VALUES('pi','api','test-model','openai_compatible','default','completed',?,5,3,1)`
	for _, ts := range []time.Time{midnight.Add(-time.Millisecond), midnight} {
		if _, err := e.db.Exec(useQuery, ts.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	for _, date := range []string{"2026-01-10", "2026-01-11"} {
		s := call[dailyStats](t, statsEngine, "stats.daily", dateRequest{date})
		if s.Attempts != 1 || !hasCount(s.InputTokens, 5) || s.Renames != 0 || s.UnknownUsage != 0 {
			t.Fatal(s)
		}
	}
}

// This opt-in test invokes the installed native CLI with a fresh auth/config directory.
// The naming request uses a loopback Responses fixture and no subscription credentials.
func TestInstalledCodexCLI(t *testing.T) {
	if os.Getenv("ATM_TEST_NATIVE_CODEX") != "1" {
		t.Skip("设置 ATM_TEST_NATIVE_CODEX=1 运行隔离的原生 CLI 测试")
	}
	binary, err := codexExecutable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "isolated-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(codexFixture))
	defer server.Close()
	args, err := codexArgs(root, Settings{CodexModel: "gpt-6-luna", Fast: true}, "Return the fixture JSON.")
	if err != nil {
		t.Fatal(err)
	}
	args = append(args[:len(args)-1], "-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="Fixture"`,
		"-c", fmt.Sprintf(`model_providers.fixture.base_url="%s"`, server.URL),
		"-c", `model_providers.fixture.wire_api="responses"`,
		"-c", "model_providers.fixture.stream_max_retries=1", "-")
	r, runErr, out, stderr := runFixtureCLI(ctx, binary, root, home, args)
	if r.status != "completed" || r.decision.Title != goodTitle || r.usage.Input == nil || *r.usage.Input != 19 {
		t.Fatalf("native CLI %v, parsed=%#v\n%s\n%s", runErr, r, out, stderr)
	}
	args = append(args[:len(args)-1], "-c", fmt.Sprintf(`model_providers.fixture.base_url="%s/refused"`, server.URL), "-")
	r, runErr, out, stderr = runFixtureCLI(ctx, binary, root, home, args)
	if !r.confirmed || r.status != "failed" || runErr == nil {
		t.Fatalf("native rejection %v, parsed=%#v\n%s\n%s", runErr, r, out, stderr)
	}
}

func runFixtureCLI(ctx context.Context, binary, root, home string, args []string) (modelResult, error, string, string) {
	cmd := codexCommand(ctx, binary, root, args...)
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home, "TEMP="+root, "TMP="+root, "TMPDIR="+root)
	cmd.Stdin = strings.NewReader(`{"fixture":true}`)
	var out, stderr limitedOutput
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	r := parseCodex(out.Bytes(), out.truncated, runErr, ctx.Err())
	return r, runErr, out.String(), stderr.String()
}

func codexFixture(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/refused/responses" {
		http.Error(w, "fixture unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path != "/responses" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(request.Tools) > 0 {
		http.Error(w, "temporary Codex exec must omit tools", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	item := `{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{\"action\":\"rename\",\"title\":\"🧩 构建｜修复路径\",\"reason\":\"fixture\"}","annotations":[]}]}`
	events := []string{
		`{"type":"response.created","response":{"id":"resp_test"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`,
		`{"type":"response.completed","response":{"id":"resp_test","usage":{"input_tokens":19,"input_tokens_details":{"cached_tokens":4},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":24}}}`,
	}
	for _, data := range events {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
}

func TestTitleRules(t *testing.T) {
	for _, title := range []string{"🧩 React｜修复", "⚙️ Codex｜配置", "📝 构建｜笔记"} {
		if reason := titleFailure(title, 15); reason.Code != "" {
			t.Errorf("拒绝 %q", title)
		}
	}
	for _, title := range []string{"🧩 路径:修复", "⚙️ 构建｜📝 修复", "📝 构建｜修复｜提交", "🧩 项目模块特别特别长｜假装在这里继续编写名字"} {
		if reason := titleFailure(title, 15); reason.Code == "" {
			t.Errorf("误接收 %q", title)
		}
	}
	if !bytes.Contains([]byte(namingPolicy), []byte("max_title_length")) {
		t.Fatal("命名规则未嵌入")
	}
}
