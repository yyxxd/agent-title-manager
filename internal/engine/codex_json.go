package engine

import (
	"bytes"
	"encoding/json"
	"strings"
)

type codexEvent struct {
	Type    string `json:"type"`
	Usage   usage  `json:"usage"`
	Tier    string `json:"service_tier"`
	Message string `json:"message"`
	Item    struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Error struct {
		Status  int    `json:"status"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type codexTrace struct {
	text, tier                    string
	usage                         usage
	failed, done, tool, confirmed bool
	itemError                     bool
}

func parseCodex(raw []byte, truncated bool, runErr, ctxErr error) modelResult {
	var trace codexTrace
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var event codexEvent
		if json.Unmarshal(line, &event) == nil {
			trace.apply(event)
		}
	}
	r := modelResult{status: "failed", usage: trace.usage.clean(), actualTier: knownTier(trace.tier)}
	switch {
	case ctxErr != nil:
		r.status = "interrupted"
		r.message = "Codex 请求超时或已取消"
	case trace.tool:
		r.status = "rejected_tool"
		r.message = "Codex 尝试调用工具，已丢弃命名结果"
	case truncated:
		r.status = "invalid_response"
		r.message = errOutputLimit.Error()
	case runErr != nil || trace.failed || trace.itemError:
		r.message = "Codex 命名失败，请检查订阅、模型与 CLI"
		r.confirmed = trace.confirmed && !trace.done && trace.text == "" && r.usage == (usage{})
	case !trace.done:
		r.status = "invalid_response"
		r.message = "Codex 未返回已完成的命名结果"
	default:
		return response([]byte(trace.text), r.usage, r.actualTier)
	}
	return r
}

func (t *codexTrace) apply(e codexEvent) {
	switch e.Type {
	case "turn.completed":
		t.done = true
		t.usage = e.Usage
		t.tier = e.Tier
	case "turn.failed", "error":
		if strings.HasPrefix(e.Message, "Reconnecting... ") {
			return
		}
		t.failed = true
		if isHTTPRefusal(e.Message) || isHTTPRefusal(e.Error.Message) {
			t.confirmed = true
		}
		switch e.Error.Status {
		case 401, 403, 429:
			t.confirmed = true
		}
		switch e.Error.Code {
		case "invalid_api_key", "insufficient_quota", "rate_limit_exceeded", "model_not_found":
			t.confirmed = true
		}
	case "item.started", "item.updated", "item.completed":
		switch e.Item.Type {
		case "agent_message":
			if e.Type == "item.completed" && !t.done {
				t.text = strings.TrimSpace(e.Item.Text)
				// CLI 的技能/MCP 告警也用 item.error 表示。有后续有效决策及 turn.completed 时可继续。
				t.itemError = false
			}
		case "reasoning", "plan", "":
		case "error":
			t.itemError = true
		default:
			t.tool = true
		}
	}
}

func isHTTPRefusal(message string) bool {
	return strings.HasPrefix(message, "unexpected status 401 Unauthorized:") ||
		strings.HasPrefix(message, "unexpected status 403 Forbidden:") ||
		strings.HasPrefix(message, "unexpected status 429 Too Many Requests:")
}
