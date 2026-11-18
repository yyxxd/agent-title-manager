package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWindowsCredentialStorageAndAPI(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	const testKey = "synthetic-credential"
	httpProvider(t, e, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Error("未收到隔离测试密钥")
		}
		_, _ = w.Write([]byte(usageReply))
	})
	s := call[Settings](t, e, "settings.save", map[string]any{"apiKey": testKey})
	if !s.HasAPIKey {
		t.Fatal("未保存密钥")
	}
	var encrypted []byte
	if err := e.db.QueryRow("SELECT api_key FROM settings").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if len(encrypted) == 0 || bytes.Contains(encrypted, []byte(testKey)) {
		t.Fatal("密钥没有被加密")
	}
	before := call[Settings](t, e, "settings.get", map[string]any{})
	raw, _ := json.Marshal(before)
	if bytes.Contains(raw, []byte(testKey)) {
		t.Fatal("响应泄露密钥")
	}
	result := call[testResult](t, e, "providers.test", providerTest{Provider: "api"})
	if !result.OK {
		t.Fatal(result)
	}
	if _, err := e.Handle(context.Background(), "settings.save", []byte(`{"apiBaseUrl":"https://example.invalid"}`)); err == nil {
		t.Fatal("保存地址时错误保留了上一个地址的密钥")
	}
	if call[Settings](t, e, "settings.save", map[string]any{"apiKey": ""}).HasAPIKey {
		t.Fatal("删除密钥失败")
	}
}

func TestWindowsDraftProviderCredentials(t *testing.T) {
	for _, f := range protocolFixtures {
		t.Run(f.protocol, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			const savedKey, newKey = "synthetic-saved-key", "synthetic-new-key"
			var expected atomic.Value
			expected.Store(savedKey)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				verifyAuthHeader(t, r, f.protocol, expected.Load().(string))
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(f.catalog))
					return
				}
				_, _ = w.Write([]byte(f.reply))
			}))
			defer server.Close()
			draft := map[string]any{"provider": "api", "apiProtocol": f.protocol, "apiBaseUrl": server.URL, "apiModel": "demo", "apiKey": savedKey}
			call[Settings](t, e, "settings.save", draft)
			delete(draft, "apiKey")
			invokeDraft(t, e, draft)

			expected.Store("")
			draft["apiKey"] = ""
			invokeDraft(t, e, draft)
			delete(draft, "apiKey")

			draft["apiBaseUrl"] = server.URL + "/edited"
			invokeDraft(t, e, draft)
			expected.Store(newKey)
			draft["apiKey"] = newKey
			invokeDraft(t, e, draft)

			expected.Store(savedKey)
			draft["apiBaseUrl"] = server.URL
			delete(draft, "apiKey")
			invokeDraft(t, e, draft)
			if s := call[Settings](t, e, "settings.get", nil); !s.HasAPIKey || s.APIBaseURL != server.URL {
				t.Fatal(s)
			}
		})
	}
}

func invokeDraft(t *testing.T, e *Engine, draft map[string]any) {
	t.Helper()
	if r := call[testResult](t, e, "providers.test", draft); !r.OK {
		t.Fatal(r)
	}
	if m := call[modelCatalog](t, e, "providers.models", draft); len(m.Items) != 1 {
		t.Fatal(m)
	}
}

func verifyAuthHeader(t *testing.T, r *http.Request, protocol, key string) {
	t.Helper()
	name, value := "Authorization", key
	switch protocol {
	case protocolAnthropic:
		name = "X-Api-Key"
		if r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("缺少 Anthropic 版本头")
		}
	case protocolGemini:
		name = "X-Goog-Api-Key"
	default:
		if key != "" {
			value = "Bearer " + key
		}
	}
	for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		expected := ""
		if header == name {
			expected = value
		}
		if r.Header.Get(header) != expected {
			t.Errorf("意外的 %s 鉴权头", header)
		}
	}
	if r.URL.RawQuery != "" {
		t.Error("密钥进入了 URL")
	}
}

func TestWindowsAPIKeyExcludedFromUpstreamErrorsAndRedirects(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	const key = "synthetic-private-key"
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r, protocolAnthropic, key)
		if r.Method == http.MethodGet {
			http.Redirect(w, r, target.URL+"?key="+key, http.StatusTemporaryRedirect)
			return
		}
		http.Error(w, key, http.StatusUnauthorized)
	}))
	defer server.Close()
	p := map[string]any{"provider": "api", "apiProtocol": protocolAnthropic, "apiBaseUrl": server.URL, "apiModel": "demo", "apiKey": key}
	call[Settings](t, e, "settings.save", p)
	delete(p, "apiKey")
	params, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for method, status := range map[string]string{"providers.models": "307", "providers.test": "401"} {
		_, err := e.Handle(context.Background(), method, params)
		if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), status) {
			t.Fatal(err)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("请求跟随了重定向")
	}
}
