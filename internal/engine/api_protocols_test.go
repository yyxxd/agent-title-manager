package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureDecision = `{\"action\":\"rename\",\"title\":\"🧩 构建｜修复路径\",\"reason\":\"goal\"}`

type protocolFixture struct {
	protocol, generation, reply, catalog, source string
}

var protocolFixtures = []protocolFixture{
	{protocolChat, "/v1/chat/completions", usageReply,
		`{"data":[{"id":"demo","name":"Demo"}]}`, "openai_compatible"},
	{protocolResponses, "/v1/responses",
		`{"status":"completed","service_tier":"flex","output":[{"type":"reasoning"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + fixtureDecision + `"}]}],"usage":{"input_tokens":50,"output_tokens":8,"input_tokens_details":{"cached_tokens":12}}}`,
		`{"data":[{"id":"demo","name":"Demo"}]}`, "openai_compatible"},
	{protocolAnthropic, "/v1/messages",
		`{"content":[{"type":"thinking","thinking":"ignore"},{"type":"text","text":"` + fixtureDecision + `"}],"usage":{"input_tokens":20,"cache_creation_input_tokens":18,"cache_read_input_tokens":12,"output_tokens":8}}`,
		`{"data":[{"id":"demo","display_name":"Demo"}]}`, "anthropic_compatible"},
	{protocolGemini, "/v1beta/models/demo:generateContent",
		`{"candidates":[{"content":{"parts":[{"text":"ignore","thought":true},{"text":"` + fixtureDecision + `"}]}}],"usageMetadata":{"promptTokenCount":50,"cachedContentTokenCount":12,"candidatesTokenCount":3,"thoughtsTokenCount":5,"totalTokenCount":58}}`,
		`{"models":[{"name":"models/demo","displayName":"Demo","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`, "gemini_compatible"},
}

func TestProviderProtocolRoundTrips(t *testing.T) {
	for _, fixture := range protocolFixtures {
		t.Run(fixture.protocol, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			e.catalog = func(context.Context, string) (subscriptionCatalog, error) {
				return subscriptionCatalog{Items: []subscriptionModel{{ID: "fixture"}}}, nil
			}
			e.generate = func(ctx context.Context, r attemptRequest, start func() error) modelResult {
				if r.provider == "codex" {
					if err := start(); err != nil {
						return modelResult{status: "interrupted"}
					}
					return modelResult{status: "failed", confirmed: true}
				}
				return e.runProvider(ctx, r, start)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture.serve(t, w, r)
			}))
			defer server.Close()
			p := endpointRequest{fixture.protocol, server.URL, "demo"}
			endpoints := call[apiEndpoints](t, e, "providers.endpoints", p)
			models := call[modelCatalog](t, e, "providers.models", p)
			if endpoints.GenerationURL != server.URL+fixture.generation || len(models.Items) != 1 || models.Items[0] != (modelItem{"demo", "Demo"}) {
				t.Fatal(endpoints, models)
			}
			if stats := call[dailyStats](t, e, "stats.daily", dateRequest{}); stats.Attempts != 0 {
				t.Fatal("模型列表进入了用量账本", stats)
			}
			testDraft := map[string]any{"provider": "api", "apiProtocol": p.APIProtocol, "apiBaseUrl": p.APIBaseURL, "apiModel": p.APIModel}
			if r := call[testResult](t, e, "providers.test", testDraft); !r.OK || r.Model != "demo" {
				t.Fatal(r)
			}
			if s := call[Settings](t, e, "settings.get", nil); s.APIProtocol != protocolChat || s.APIBaseURL != "" {
				t.Fatal(s)
			}
			p.APIBaseURL += fixture.generation
			testDraft["apiBaseUrl"] = p.APIBaseURL
			if s := call[Settings](t, e, "settings.save", testDraft); s.APIBaseURL != p.APIBaseURL {
				t.Fatal(s.APIBaseURL, p.APIBaseURL)
			}
			if full := call[apiEndpoints](t, e, "providers.endpoints", p); full != endpoints {
				t.Fatal(full, endpoints)
			}
			call[modelCatalog](t, e, "providers.models", p)
			verifyProtocolNaming(t, e, fixture.source, false)
			call[Settings](t, e, "settings.save", map[string]any{"provider": "codex", "codexModel": "fixture", "fallbackEnabled": true})
			verifyProtocolNaming(t, e, fixture.source, true)
		})
	}
}

func TestProviderProtocolHTTPFailures(t *testing.T) {
	for _, f := range protocolFixtures {
		t.Run(f.protocol, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != f.generation {
					t.Error(r.Method, r.URL)
				}
				http.Error(w, "private upstream response", http.StatusForbidden)
			}))
			defer server.Close()
			params, err := json.Marshal(map[string]any{
				"provider": "api", "apiProtocol": f.protocol, "apiBaseUrl": server.URL, "apiModel": "demo",
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Handle(context.Background(), "providers.test", params)
			if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "private") {
				t.Fatal(err)
			}
			stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
			if stats.TestAttempts != 1 || stats.UnknownUsage != 1 || stats.InputTokens != nil || stats.OutputTokens != nil || stats.CachedTokens != nil {
				t.Fatal(stats)
			}
		})
	}
}

func (f protocolFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	modelsPath := "/v1/models"
	if f.protocol == protocolGemini {
		modelsPath = "/v1beta/models"
	}
	if r.Method == http.MethodGet && r.URL.Path == modelsPath {
		_, _ = w.Write([]byte(f.catalog))
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != f.generation || r.URL.RawQuery != "" {
		t.Error(r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	var fields []string
	switch f.protocol {
	case protocolChat:
		fields = []string{"model", "messages", "max_tokens"}
	case protocolResponses:
		fields = []string{"model", "instructions", "input", "max_output_tokens", "store"}
	case protocolAnthropic:
		fields = []string{"model", "system", "messages", "max_tokens"}
	case protocolGemini:
		fields = []string{"systemInstruction", "contents", "generationConfig"}
	}
	for _, field := range fields {
		if len(body[field]) == 0 {
			t.Errorf("请求缺少字段 %s", field)
		}
	}
	if f.protocol == protocolResponses && string(body["store"]) != "false" {
		t.Error("Responses 未关闭存储")
	}
	if f.protocol == protocolAnthropic && r.Header.Get("anthropic-version") != "2023-06-01" {
		t.Error("Anthropic 请求缺少版本头")
	}
	_, _ = w.Write([]byte(f.reply))
}

func verifyProtocolNaming(t *testing.T, e *Engine, source string, fallback bool) {
	t.Helper()
	eventID := "primary"
	wantAttempts := 1
	if fallback {
		eventID, wantAttempts = "fallback", 2
	}
	p := event(eventID, "")
	p.SessionID = eventID
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool {
		r := latest(t, e)
		return r.SessionID == eventID && (r.Status == "candidate" || r.Status == "failed")
	})
	r := latest(t, e)
	if r.Status != "candidate" || r.Title != goodTitle || len(r.Attempts) != wantAttempts {
		t.Fatal(r)
	}
	attempt := r.Attempts[wantAttempts-1]
	if attempt.Source != source || !hasCount(attempt.InputTokens, 50) || !hasCount(attempt.OutputTokens, 8) || !hasCount(attempt.CachedTokens, 12) {
		t.Fatal(attempt)
	}
}

func TestProviderEndpoints(t *testing.T) {
	const host = "https://example.invalid"
	for _, tc := range []struct {
		protocol, base, model, generation, models string
	}{
		{protocolChat, "", "demo", "/v1/chat/completions", "/v1/models"},
		{protocolChat, "/api/", "", "/api/chat/completions", "/api/models"},
		{protocolChat, "/proxy/v1beta/openai", "", "/proxy/v1beta/openai/chat/completions", "/proxy/v1beta/openai/models"},
		{protocolChat, "/proxy/v1/chat/completions", "", "/proxy/v1/chat/completions", "/proxy/v1/models"},
		{protocolResponses, "", "demo", "/v1/responses", "/v1/models"},
		{protocolResponses, "/proxy", "", "/proxy/responses", "/proxy/models"},
		{protocolResponses, "/proxy/chat/completions/bridge", "", "/proxy/chat/completions/bridge/responses", "/proxy/chat/completions/bridge/models"},
		{protocolResponses, "/proxy/v2/responses", "", "/proxy/v2/responses", "/proxy/v2/models"},
		{protocolAnthropic, "", "demo", "/v1/messages", "/v1/models"},
		{protocolAnthropic, "/claude", "", "/claude/v1/messages", "/claude/v1/models"},
		{protocolAnthropic, "/claude/v1beta", "", "/claude/v1beta/messages", "/claude/v1beta/models"},
		{protocolAnthropic, "/claude/messages", "", "/claude/messages", "/claude/models"},
		{protocolGemini, "", "models/demo", "/v1beta/models/demo:generateContent", "/v1beta/models"},
		{protocolGemini, "/proxy", "publisher/demo", "/proxy/models/publisher/demo:generateContent", "/proxy/models"},
		{protocolGemini, "/proxy/responses-adapter", "demo", "/proxy/responses-adapter/models/demo:generateContent", "/proxy/responses-adapter/models"},
		{protocolGemini, "/proxy/v1beta/models", "", "/proxy/v1beta/models/{model}:generateContent", "/proxy/v1beta/models"},
		{protocolGemini, "/proxy/v1/models/demo:generateContent", "", "/proxy/v1/models/demo:generateContent", "/proxy/v1/models"},
		{protocolGemini, "/proxy/v1/models/demo:generateContent", "new", "/proxy/v1/models/new:generateContent", "/proxy/v1/models"},
	} {
		got, err := resolveEndpoints(tc.protocol, host+tc.base, tc.model, false)
		want := apiEndpoints{host + tc.generation, host + tc.models}
		if got != want || err != nil {
			t.Errorf("%s %s %s: got %+v, %v; want %+v", tc.protocol, tc.base, tc.model, got, err, want)
		}
	}
}

func TestProviderConflictingFullEndpoints(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("协议冲突时发出了 HTTP 请求 %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	const want = "API 协议与地址不匹配，请切换协议或修改 API 地址"
	for _, fixture := range protocolFixtures {
		for _, selection := range protocolFixtures {
			if fixture.protocol == selection.protocol {
				continue
			}
			t.Run(fixture.protocol+"-to-"+selection.protocol, func(t *testing.T) {
				base := server.URL + "/proxy" + fixture.generation + "/"
				assertProviderEndpointMismatch(t, e, selection.protocol, base, want)
			})
		}
	}
	if stats := call[dailyStats](t, e, "stats.daily", dateRequest{}); stats.Attempts != 0 {
		t.Fatal(stats)
	}
}

func assertProviderEndpointMismatch(t *testing.T, e *Engine, protocol, base, want string) {
	t.Helper()
	ctx := context.Background()
	p, err := json.Marshal(map[string]any{"provider": "api", "apiProtocol": protocol, "apiBaseUrl": base, "apiModel": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"providers.endpoints", "providers.models", "providers.test", "settings.save"} {
		if _, err := e.Handle(ctx, method, p); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", method, err, want)
		}
	}
	s := Settings{APIProtocol: protocol, APIBaseURL: base, APIModel: "demo"}
	result := runAPI(ctx, s, nil, "", nil, func() error {
		t.Error("协议冲突时开始了真实命名尝试")
		return nil
	})
	if result.status != "failed" || result.message != want {
		t.Errorf("真实命名: %+v, want %q", result, want)
	}
}

func TestLegacyProviderMigration(t *testing.T) {
	for _, path := range []string{"", "/relay", "/relay/v1", "/relay/chat/completions", "/relay/v1/responses", "/relay/v1/messages", "/relay/v1beta/models/demo:generateContent"} {
		t.Run("旧地址"+path, func(t *testing.T) {
			generationPath := path
			if !strings.HasSuffix(path, "/chat/completions") {
				generationPath += "/chat/completions"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != generationPath {
					t.Error(r.Method, r.URL.Path, generationPath)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(usageReply))
			}))
			defer server.Close()
			e := openLegacyEngine(t, t.TempDir(), server.URL+path)
			s := call[Settings](t, e, "settings.get", nil)
			if s.APIProtocol != protocolChat || !s.apiLegacy || s.APIModel != "demo" {
				t.Fatal(s)
			}
			p := endpointRequest{protocolChat, s.APIBaseURL, "other"}
			got := call[apiEndpoints](t, e, "providers.endpoints", p)
			if got.GenerationURL != server.URL+generationPath {
				t.Fatal(got, generationPath)
			}
			if result := call[testResult](t, e, "providers.test", providerTest{}); !result.OK {
				t.Fatal(result)
			}
			if s := call[Settings](t, e, "settings.save", p); !s.apiLegacy {
				t.Fatal("保存模型时丢失旧路径")
			}
			p.APIBaseURL += "/new"
			if s := call[Settings](t, e, "settings.save", p); s.apiLegacy || s.APIBaseURL != p.APIBaseURL {
				t.Fatal(s)
			}
		})
	}
}

func openLegacyEngine(t *testing.T, dir, base string) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema + schemaV2); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE settings SET provider='api',api_url=?,api_model='demo'", base)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	return engineAt(t, dir, nil)
}

func TestProviderPartialAndRejectedUsage(t *testing.T) {
	for _, tc := range []struct {
		protocol, reply string
		want            [3]int64
	}{
		{protocolChat, `{"usage":{"prompt_tokens":0}}`, [3]int64{0, -1, -1}},
		{protocolResponses, `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"` + fixtureDecision + `"}]}],"usage":{"output_tokens":9}}`, [3]int64{-1, 9, -1}},
		{protocolAnthropic, `{"usage":{"input_tokens":15,"cache_creation_input_tokens":4,"output_tokens":0}}`, [3]int64{19, 0, -1}},
		{protocolAnthropic, `{"usage":{"cache_read_input_tokens":7}}`, [3]int64{-1, -1, 7}},
		{protocolGemini, `{"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":0,"cachedContentTokenCount":0,"thoughtsTokenCount":4}}`, [3]int64{15, 4, 0}},
		{protocolGemini, `{"usageMetadata":{"promptTokenCount":15,"thoughtsTokenCount":4}}`, [3]int64{15, -1, -1}},
	} {
		result := parseAPI(tc.protocol, strings.NewReader(tc.reply))
		if result.status != "invalid_response" {
			t.Error(tc.protocol, result)
		}
		for i, count := range []*int64{result.usage.Input, result.usage.Output, result.usage.Cached} {
			if want := tc.want[i]; (want < 0 && count != nil) || (want >= 0 && !hasCount(count, want)) {
				t.Errorf("%s: count %d got %v, want %d", tc.protocol, i, count, want)
			}
		}
	}
}

func TestProviderModelPagination(t *testing.T) {
	for _, tc := range []struct {
		protocol, cursor, first, second string
	}{
		{protocolAnthropic, "after_id", `{"data":[{"id":"one"}],"has_more":true,"last_id":"one"}`, `{"data":[{"id":"one"},{"id":"two"}]}`},
		{protocolGemini, "pageToken", `{"models":[{"name":"models/one"}],"nextPageToken":"one"}`, `{"models":[{"name":"models/one"},{"name":"models/two"}]}`},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error(r.Method)
				}
				switch r.URL.RawQuery {
				case "":
					_, _ = w.Write([]byte(tc.first))
				case tc.cursor + "=one":
					_, _ = w.Write([]byte(tc.second))
				default:
					t.Error(r.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			e := engineAt(t, t.TempDir(), nil)
			models := call[modelCatalog](t, e, "providers.models", modelsRequest{tc.protocol, server.URL, nil})
			if len(models.Items) != 2 || models.Items[0].ID != "one" || models.Items[1].ID != "two" {
				t.Fatal(models)
			}
		})
	}
}

func TestProviderListTimeoutAndInvalidEndpoint(t *testing.T) {
	for _, base := range []string{"https://user:secret@example.invalid", "https://example.invalid/?key=secret", "https://example.invalid/#secret", "http://example.invalid"} {
		_, err := resolveEndpoints(protocolChat, base, "", false)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fetchModels(ctx, server.URL, protocolChat, nil); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatal(err)
	}
}
