package engine

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const decisionSchema = `{"type":"object","properties":{"action":{"type":"string","enum":["keep","rename"]},"title":{"type":"string"},"reason":{"type":"string"}},"required":["action","title","reason"],"additionalProperties":false}`

//go:embed naming.md
var namingPolicy string

//go:embed naming-short.md
var shortNamingPolicy string

//go:embed naming-detailed.md
var detailedNamingPolicy string

type providerTest struct {
	nativePatch
	Provider       string  `json:"provider"`
	APIProtocol    *string `json:"apiProtocol"`
	APIBaseURL     *string `json:"apiBaseUrl"`
	APIModel       *string `json:"apiModel"`
	APIKey         *string `json:"apiKey"`
	MaxTitleLength *int    `json:"maxTitleLength"`
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
	p.nativePatch.apply(&s)
	patch(&s.MaxTitleLength, p.MaxTitleLength)
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
	} else if err := e.checkSubscription(ctx, s); err != nil {
		return testResult{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	r, err := e.invoke(ctx, attemptRequest{agent: "test", provider: s.Provider, settings: s, secret: secret, payload: []byte(`{"connectivity_test":true}`)})
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
	if r.actualModel != "" {
		model = r.actualModel
	}
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
	policy, prompt, err := providerPrompt(req.payload, req.settings.MaxTitleLength, req.feedback)
	if err != nil {
		return modelResult{status: "invalid_response"}
	}
	switch req.provider {
	case "api":
		return runAPI(ctx, req.settings, req.secret, policy, prompt, start)
	case "claude":
		return e.runClaude(ctx, req.settings, policy, prompt, start)
	default:
		return e.runCodex(ctx, req.settings, policy, prompt, start)
	}
}

type titleRetry struct {
	PreviousTitle  string      `json:"previous_title"`
	FailureCode    failureCode `json:"failure_code"`
	ActualLength   int         `json:"actual_length"`
	MaxTitleLength int         `json:"max_title_length"`
}

func correctionFeedback(result modelResult, base string, limit int) *titleRetry {
	if result.status != "completed" || result.decision.Action != "rename" || result.decision.Title == base {
		return nil
	}
	title := result.decision.Title
	reason := titleFailure(title, acceptedTitleLength(limit))
	if reason.Code == "" {
		return nil
	}
	return &titleRetry{title, reason.Code, utf8.RuneCountInString(title), limit}
}

func providerPrompt(payload []byte, maxLength int, feedback *titleRetry) (string, []byte, error) {
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
	if feedback != nil {
		policy += "\n上一次改名未通过校验。retry_feedback.previous_title 是上一次标题的原文，actual_length 是它的 Unicode 码点数。"
		switch feedback.FailureCode {
		case failureTooLong:
			policy += "压缩原有对象和目标，将码点数严格控制在 retry_feedback.max_title_length 以内。emoji 的每个码点、空格和全角｜都计入长度。使用更短的自然措辞，避免截断单词。"
		case failureCategory:
			policy += "类别 emoji 不合法。选择规则列出的一个 emoji，后接一个空格，再写对象｜目标。"
		case failureSeparator:
			policy += "对象与目标的分隔符不合法。只用一个全角｜分隔，分隔符两侧均有内容且无多余空格。"
		case failureTitleFormat:
			policy += "标题格式不合法。去掉首尾空白、控制字符和不允许的符号，严格使用「emoji 空格 对象｜目标」。"
		}
		policy += "若 current_title 为空且主题明确，仍用 rename 返回修正后的标题。"
		data["retry_feedback"] = feedback
	}
	data["max_title_length"] = maxLength
	prompt, err := json.Marshal(data)
	return policy, prompt, err
}
