package desktop

import (
	"context"
	"encoding/json"
	"testing"
)

func TestServiceProviderRPCs(t *testing.T) {
	var method string
	s := Service{backend: Backend{Engine: func(_ context.Context, name string, body json.RawMessage) (any, error) {
		method = name
		if string(body) != `{"apiProtocol":"gemini"}` {
			t.Error(string(body))
		}
		return map[string]string{"apiProtocol": "gemini", "apiKey": "private"}, nil
	}}}
	for _, name := range []string{"settings.get", "settings.save", "providers.models", "providers.endpoints", "providers.test"} {
		v, err := s.Call(context.Background(), name, `{"apiProtocol":"gemini"}`)
		if err != nil || method != name {
			t.Fatal(name, method, err)
		}
		if name == "settings.get" || name == "settings.save" {
			if string(v.(json.RawMessage)) != `{"apiProtocol":"gemini"}` {
				t.Fatal("设置响应缺少 API 协议或暴露了密钥")
			}
		}
	}
}

func TestServicePluginRPCs(t *testing.T) {
	var method string
	s := Service{backend: Backend{Plugins: func(_ context.Context, name string, _ json.RawMessage) (any, error) {
		method = name
		return map[string]string{"ok": name}, nil
	}}}
	for _, name := range []string{"plugins.list", "plugins.install", "plugins.update", "plugins.enable", "plugins.disable"} {
		if _, err := s.Call(context.Background(), name, `{"id":"codex"}`); err != nil || method != name {
			t.Fatalf("expected %s to route, got method=%s err=%v", name, method, err)
		}
	}
	if _, err := s.Call(context.Background(), "plugins.restore", `{"id":"codex"}`); err == nil {
		t.Fatal("expected plugins.restore to be unsupported")
	}
}
