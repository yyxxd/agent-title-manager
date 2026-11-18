package engine

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
)

const decisionSchema = `{"type":"object","properties":{"action":{"type":"string","enum":["keep","rename"]},"title":{"type":"string"},"reason":{"type":"string"}},"required":["action","title","reason"],"additionalProperties":false}`

//go:embed naming.md
var namingPolicy string

type providerTest struct {
	Provider   string  `json:"provider"`
	APIBaseURL *string `json:"apiBaseUrl"`
	APIModel   *string `json:"apiModel"`
	APIKey     *string `json:"apiKey"`
	CodexModel *string `json:"codexModel"`
	Fast       *bool   `json:"fast"`
}
type testResult struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	OK       bool   `json:"ok"`
}

func (e *Engine) testProvider(ctx context.Context, p providerTest) (testResult, error) {
	s, secret, err := loadSettings(ctx, e.db)
	if err != nil {
		return testResult{}, err
	}
	oldURL := s.APIBaseURL
	if p.Provider != "" {
		s.Provider = p.Provider
	}
	patch(&s.CodexModel, p.CodexModel)
	patch(&s.Fast, p.Fast)
	patch(&s.APIBaseURL, p.APIBaseURL)
	patch(&s.APIModel, p.APIModel)
	if err := validateSettings(&s); err != nil {
		return testResult{}, err
	}
	if s.APIBaseURL != oldURL {
		secret = nil
	}
	if p.APIKey != nil && s.Provider == "api" {
		if !validString(*p.APIKey, 4096) {
			return testResult{}, errors.New("API 密钥格式无效")
		}
		secret, err = credential([]byte(*p.APIKey), false)
		if err != nil {
			return testResult{}, errors.New("无法处理 API 密钥")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	r, err := e.invoke(ctx, attemptRequest{nil, "test", s.Provider, s, secret, []byte(`{"connectivity_test":true}`)})
	if err != nil {
		return testResult{}, err
	}
	if r.status != "completed" {
		if r.message == "" {
			r.message = "无法完成命名服务连接测试"
		}
		return testResult{}, errors.New(r.message)
	}
	model, _, _ := providerInfo(s.Provider, s)
	return testResult{s.Provider, model, true}, nil
}

func (e *Engine) runProvider(ctx context.Context, req attemptRequest, start func() error) modelResult {
	policy := namingPolicy + "\n可信配置：初始非空的未知标题与明确手动标题由宿主保护。max_title_length 见输入。"
	var data map[string]any
	if err := json.Unmarshal(req.payload, &data); err != nil {
		return modelResult{status: "invalid_response"}
	}
	if data["connectivity_test"] == true {
		policy = `这是一个连接测试。只返回 {"action":"keep","title":"","reason":""}。`
	}
	data["max_title_length"] = req.settings.MaxTitleLength
	prompt, err := json.Marshal(data)
	if err != nil {
		return modelResult{status: "invalid_response"}
	}
	if req.provider == "api" {
		return runAPI(ctx, req.settings, req.secret, policy, prompt, start)
	}
	return e.runCodex(ctx, req.settings, policy, prompt, start)
}
