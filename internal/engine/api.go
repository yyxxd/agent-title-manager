package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

var apiClient = &http.Client{
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

func runAPI(ctx context.Context, s Settings, secret []byte, policy string, input []byte, start func() error) modelResult {
	if !apiReady(s) {
		return modelResult{status: "failed", message: "请先填写 API 地址和模型"}
	}
	endpoints, err := resolveEndpoints(s.APIProtocol, s.APIBaseURL, s.APIModel, s.apiLegacy)
	if err != nil {
		return modelResult{status: "failed", message: err.Error()}
	}
	key, err := credential(secret, true)
	if err != nil {
		return modelResult{status: "failed", message: "无法解密当前用户的 API 密钥，请重新保存"}
	}
	defer clear(key)
	request, err := makeAPIRequest(ctx, endpoints.GenerationURL, s.APIProtocol, s.APIModel, policy, string(input), key)
	if err != nil {
		return modelResult{status: "failed", message: "无法构造 API 请求"}
	}
	if err := start(); err != nil {
		return modelResult{status: "interrupted"}
	}
	res, err := apiClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return modelResult{status: "interrupted", message: "API 请求超时或已取消"}
		}
		return modelResult{status: "failed", message: "自定义 API 请求失败，请检查地址与网络"}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return modelResult{status: "failed", message: "自定义 API 返回 HTTP " + strconv.Itoa(res.StatusCode)}
	}
	return parseAPI(s.APIProtocol, res.Body)
}

func makeAPIRequest(ctx context.Context, endpoint, protocol, model, policy, input string, key []byte) (*http.Request, error) {
	payload, err := json.Marshal(generationPayload(protocol, model, policy, input))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAPIHeaders(req, protocol, key)
	return req, nil
}

func setAPIHeaders(req *http.Request, protocol string, key []byte) {
	switch protocol {
	case protocolAnthropic:
		req.Header.Set("anthropic-version", "2023-06-01")
		if len(key) > 0 {
			req.Header.Set("x-api-key", string(key))
		}
	case protocolGemini:
		if len(key) > 0 {
			req.Header.Set("x-goog-api-key", string(key))
		}
	default:
		if len(key) > 0 {
			req.Header.Set("Authorization", "Bearer "+string(key))
		}
	}
}

func parseAPI(protocol string, body io.Reader) modelResult {
	const limit = 256 << 10
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return modelResult{status: "interrupted", message: "无法完整读取 API 响应"}
	}
	if len(raw) > limit {
		return modelResult{status: "invalid_response", message: errOutputLimit.Error()}
	}
	reply := emptyAPIReply(protocol)
	if json.Unmarshal(raw, reply) != nil {
		return modelResult{status: "invalid_response", message: "自定义 API 返回的 JSON 无效"}
	}
	return reply.result()
}

func apiDecision(text string, tokens usage, tier string) modelResult {
	tokens = tokens.clean()
	if text == "" {
		return modelResult{status: "invalid_response", usage: tokens, actualTier: knownTier(tier), message: "自定义 API 未返回命名内容"}
	}
	return response([]byte(text), tokens, tier)
}
