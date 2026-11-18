package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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
