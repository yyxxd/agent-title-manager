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

//go:embed naming-short.md
var shortNamingPolicy string

//go:embed naming-detailed.md
var detailedNamingPolicy string

type providerTest struct {
	Provider    string  `json:"provider"`
	APIProtocol *string `json:"apiProtocol"`
	APIBaseURL  *string `json:"apiBaseUrl"`
	APIModel    *string `json:"apiModel"`
	APIKey      *string `json:"apiKey"`
	CodexModel  *string `json:"codexModel"`
	Fast        *bool   `json:"fast"`
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
	saved := s
	if p.Provider != "" {
		s.Provider = p.Provider
	}
	patch(&s.CodexModel, p.CodexModel)
	patch(&s.Fast, p.Fast)
	patch(&s.APIProtocol, p.APIProtocol)
	patch(&s.APIBaseURL, p.APIBaseURL)
	patch(&s.APIModel, p.APIModel)
	s.apiLegacy = saved.useLegacy(s.APIProtocol, s.APIBaseURL)
	if err := validateSettings(&s); err != nil {
		return testResult{}, err
	}
	if s.Provider == "api" {
		secret, err = draftAPIKey(saved, s.APIBaseURL, secret, p.APIKey)
		if err != nil {
			return testResult{}, err
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

func draftAPIKey(saved Settings, base string, secret []byte, key *string) ([]byte, error) {
	if key != nil {
		return protectAPIKey(*key)
	}
	if !saved.sameAPIAddress(base) {
		return nil, nil
	}
	return secret, nil
}

func (e *Engine) runProvider(ctx context.Context, req attemptRequest, start func() error) modelResult {
	policy, prompt, err := providerPrompt(req.payload, req.settings.MaxTitleLength)
	if err != nil {
		return modelResult{status: "invalid_response"}
	}
	if req.provider == "api" {
		return runAPI(ctx, req.settings, req.secret, policy, prompt, start)
	}
	return e.runCodex(ctx, req.settings, policy, prompt, start)
}

func providerPrompt(payload []byte, maxLength int) (string, []byte, error) {
	var preset string
	switch maxLength {
	case shortTitleLength:
		preset = shortNamingPolicy
	case detailedTitleLength:
		preset = detailedNamingPolicy
	default:
		return "", nil, errors.New("标题长度只能选择简短或详细预设")
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil || data == nil {
		return "", nil, errors.New("命名输入必须为 JSON 对象")
	}
	policy := namingPolicy + "\n" + preset
	if data["connectivity_test"] == true {
		policy = `这是一个连接测试。只返回 {"action":"keep","title":"","reason":""}。`
	}
	data["max_title_length"] = maxLength
	prompt, err := json.Marshal(data)
	return policy, prompt, err
}
