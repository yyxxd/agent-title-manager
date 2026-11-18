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

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Tier  string `json:"service_tier"`
	Usage struct {
		Input   *int64 `json:"prompt_tokens"`
		Output  *int64 `json:"completion_tokens"`
		Details struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func runAPI(ctx context.Context, s Settings, secret []byte, policy string, input []byte, start func() error) modelResult {
	if !apiReady(s) {
		return modelResult{status: "failed", message: "请先填写 API 地址和模型"}
	}
	endpoint, err := chatEndpoint(s.APIBaseURL)
	if err != nil {
		return modelResult{status: "failed", message: err.Error()}
	}
	key, err := credential(secret, true)
	if err != nil {
		return modelResult{status: "failed", message: "无法解密当前用户的 API 密钥，请重新保存"}
	}
	defer clear(key)
	request, err := makeAPIRequest(ctx, endpoint, s.APIModel, policy, string(input), key)
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
	return parseAPI(res.Body)
}

func makeAPIRequest(ctx context.Context, endpoint, model, policy, input string, key []byte) (*http.Request, error) {
	payload, err := json.Marshal(chatRequest{model, []chatMessage{{"system", policy}, {"user", input}}, 512})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if len(key) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(key))
	}
	return req, nil
}

func parseAPI(body io.Reader) modelResult {
	const limit = 256 << 10
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return modelResult{status: "interrupted", message: "无法完整读取 API 响应"}
	}
	if len(raw) > limit {
		return modelResult{status: "invalid_response", message: errOutputLimit.Error()}
	}
	var data chatResponse
	if json.Unmarshal(raw, &data) != nil {
		return modelResult{status: "invalid_response", message: "自定义 API 返回的 JSON 无效"}
	}
	u := usage{data.Usage.Input, data.Usage.Output, data.Usage.Details.Cached}.clean()
	if len(data.Choices) == 0 {
		return modelResult{status: "invalid_response", usage: u, actualTier: knownTier(data.Tier), message: "自定义 API 未返回命名内容"}
	}
	return response([]byte(data.Choices[0].Message.Content), u, data.Tier)
}
