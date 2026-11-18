package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

const nativeCodexProvider = `model_provider="openai"`

func codexModels(ctx context.Context, dir string) ([]subscriptionModel, error) {
	binary, err := codexExecutable()
	if err != nil {
		return nil, errors.New("找不到原生 Codex CLI")
	}
	if auth := codexAuth(ctx, binary, dir); auth.status != "" {
		return nil, subscriptionAuthError(auth)
	}
	cmd := codexCommand(ctx, binary, dir, "app-server", "--listen", "stdio://",
		"-c", nativeCodexProvider, "-c", fileAuth, "-c", `openai_base_url=""`,
		"-c", `chatgpt_base_url="https://chatgpt.com/backend-api/"`,
		"-c", "mcp_servers={}", "-c", "analytics.enabled=false",
		"-c", "sqlite_home="+strconv.Quote(dir), "-c", "log_dir="+strconv.Quote(dir),
		"--disable", "hooks", "--disable", "plugins", "--disable", "apps")
	return readCLIStream(cmd, readCodexCatalog)
}

type codexRPC struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func codexCall(s cliStream, id int, method string, params any, result any) error {
	if err := s.send(codexRPC{ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	reply, err := readCLIReply(s, func(frame codexRPC) bool { return frame.ID == id })
	if err != nil || (len(reply.Error) > 0 && string(reply.Error) != "null") || len(reply.Result) == 0 {
		return errCatalogProtocol
	}
	if result != nil && json.Unmarshal(reply.Result, result) != nil {
		return errCatalogProtocol
	}
	return nil
}

func readCodexCatalog(s cliStream) ([]subscriptionModel, error) {
	if err := codexCall(s, 1, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "agent_title_manager", "version": "1"},
	}, nil); err != nil {
		return nil, err
	}
	if err := s.send(codexRPC{Method: "initialized"}); err != nil {
		return nil, err
	}
	if err := checkCodexCatalogConfig(s); err != nil {
		return nil, err
	}
	return readCodexModelPages(s)
}

func checkCodexCatalogConfig(s cliStream) error {
	var reply struct {
		Config struct {
			Provider  string `json:"model_provider"`
			LocalPath string `json:"model_catalog_json"`
			BaseURL   string `json:"openai_base_url"`
			Providers map[string]struct {
				CatalogURL string `json:"model_catalog_url"`
			} `json:"model_providers"`
		} `json:"config"`
	}
	if err := codexCall(s, 2, "config/read", map[string]bool{"includeLayers": false}, &reply); err != nil {
		return err
	}
	c := reply.Config
	if c.Provider != "openai" || c.LocalPath != "" || c.BaseURL != "" || c.Providers["openai"].CatalogURL != "" {
		return errors.New("Codex CLI 使用了自定义模型目录或地址，无法确认原生订阅目录")
	}
	return nil
}

type codexCatalogModel struct {
	Model   string `json:"model"`
	Name    string `json:"displayName"`
	Hidden  bool   `json:"hidden"`
	Default string `json:"defaultReasoningEffort"`
	Efforts []struct {
		Value string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func readCodexModelPages(s cliStream) ([]subscriptionModel, error) {
	var items []subscriptionModel
	seen := make(map[string]bool)
	params := map[string]any{"limit": 100, "includeHidden": false}
	for id := 3; id < 103; id++ {
		var page struct {
			Models []codexCatalogModel `json:"data"`
			Cursor string              `json:"nextCursor"`
		}
		if err := codexCall(s, id, "model/list", params, &page); err != nil || page.Models == nil {
			return nil, errCatalogProtocol
		}
		for _, entry := range page.Models {
			if entry.Hidden {
				continue
			}
			model := subscriptionModel{ID: entry.Model, Name: entry.Name, DefaultEffort: entry.Default}
			for _, effort := range entry.Efforts {
				model.Efforts = append(model.Efforts, effort.Value)
			}
			items = append(items, model)
		}
		if page.Cursor == "" {
			return items, nil
		}
		if seen[page.Cursor] {
			break
		}
		seen[page.Cursor] = true
		params["cursor"] = page.Cursor
	}
	return nil, errCatalogProtocol
}
