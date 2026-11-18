package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
)

func (e *Engine) runClaude(ctx context.Context, s Settings, policy string, input []byte, start func() error) modelResult {
	if s.ClaudeModel == "" {
		return modelResult{status: "failed", message: "请先选择 Claude Code 命名模型", failure: failureModelSelection}
	}
	binary, err := claudeExecutable()
	if err != nil {
		return modelResult{status: "failed", message: "找不到原生 Claude Code CLI"}
	}
	dir, err := os.MkdirTemp(e.dir, "claude-exec-")
	if err != nil {
		return modelResult{status: "failed", message: "无法创建临时命名目录"}
	}
	defer os.RemoveAll(dir)
	if err := checkClaudeManagedSettings(); err != nil {
		return modelResult{status: "failed", message: err.Error()}
	}
	if auth := claudeAuth(ctx, binary, dir); auth.status != "" {
		return auth
	}
	if err := checkClaudeManagedSettings(); err != nil {
		return modelResult{status: "failed", message: err.Error()}
	}
	return runClaudeCommand(ctx, binary, dir, claudeNamingArgs(s, policy), input, start)
}

func runClaudeCommand(ctx context.Context, binary, dir string, args []string, input []byte, start func() error) modelResult {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := claudeCommand(child, binary, dir, args...)
	cmd.Stdin = bytes.NewReader(input)
	out := claudeOutput{cancel: cancel}
	cmd.Stdout = &out
	if err := start(); err != nil {
		return modelResult{status: "interrupted"}
	}
	err := cmd.Run()
	r := parseClaude(out.Bytes(), out.truncated, err, ctx.Err())
	if out.unconfirmedRetry && ctx.Err() == nil {
		r.message = "Claude Code 请求结果未确认，已中止自动重试"
	}
	return r
}

type claudeOutput struct {
	limitedOutput
	offset           int
	cancel           func()
	unconfirmedRetry bool
}

func (o *claudeOutput) Write(data []byte) (int, error) {
	n, err := o.limitedOutput.Write(data)
	if o.truncated {
		o.cancel()
	}
	for {
		line, _, full := bytes.Cut(o.Bytes()[o.offset:], []byte{'\n'})
		if !full {
			return n, err
		}
		o.offset += len(line) + 1
		var event struct {
			Type       string `json:"type"`
			Subtype    string `json:"subtype"`
			HTTPStatus *int   `json:"error_status"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "system" && event.Subtype == "api_retry" &&
			(event.HTTPStatus == nil || *event.HTTPStatus == http.StatusRequestTimeout || *event.HTTPStatus >= 500) {
			o.unconfirmedRetry = true
			o.cancel()
		}
	}
}

func claudeNamingArgs(s Settings, policy string) []string {
	args := append(claudePrintArgs(), "--output-format", "stream-json", "--verbose", "--json-schema", decisionSchema,
		"--system-prompt", policy, "--model", s.ClaudeModel, "--max-turns", "1")
	if s.ClaudeEffort != "" {
		args = append(args, "--effort", s.ClaudeEffort)
	}
	return args
}

type claudeFrame struct {
	Type           string                     `json:"type"`
	Subtype        string                     `json:"subtype"`
	Error          string                     `json:"error"`
	IsError        *bool                      `json:"is_error"`
	StartupFailure string                     `json:"startup_failure_reason"`
	Structured     json.RawMessage            `json:"structured_output"`
	ModelUsage     map[string]json.RawMessage `json:"modelUsage"`
	Usage          struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
		Read   *int64 `json:"cache_read_input_tokens"`
		Write  *int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func parseClaude(out []byte, truncated bool, runErr, ctxErr error) modelResult {
	var result claudeFrame
	var apiError string
	for line := range bytes.SplitSeq(out, []byte{'\n'}) {
		var frame claudeFrame
		if json.Unmarshal(line, &frame) != nil {
			continue
		}
		switch frame.Type {
		case "assistant":
			if frame.Error != "" {
				apiError = frame.Error
			}
		case "result":
			result = frame
		}
	}
	u := result.tokenUsage()
	model := result.actualModel()
	if ctxErr != nil {
		return modelResult{status: "interrupted", message: "Claude Code 命名请求超时或已取消", failure: contextFailure(ctxErr), usage: u, actualModel: model}
	}
	if !truncated && runErr == nil && result.Subtype == "success" && result.IsError != nil && !*result.IsError {
		r := response(result.Structured, u, "")
		r.actualModel = model
		return r
	}
	r := claudeFailure(apiError, result.Subtype)
	r.usage, r.actualModel = u, model
	return r
}

func (f claudeFrame) actualModel() string {
	if len(f.ModelUsage) == 1 {
		for model := range f.ModelUsage {
			if validNativeValue(model) {
				return model
			}
		}
	}
	return ""
}

func (f claudeFrame) tokenUsage() usage {
	if f.StartupFailure != "" {
		return usage{}
	}
	input := cleanTokens(f.Usage.Input)
	read, write := cleanTokens(f.Usage.Read), cleanTokens(f.Usage.Write)
	// Claude 的 input_tokens 不包含缓存读取和创建。
	if input != nil && read != nil && write != nil {
		total := *input + *read + *write
		input = &total
	} else {
		input = nil
	}
	return usage{Input: input, Output: f.Usage.Output, Cached: read}.clean()
}

func claudeFailure(apiError, subtype string) modelResult {
	r := modelResult{status: "failed", failure: failureRequest, message: "Claude Code 未完成命名请求"}
	switch apiError {
	case "authentication_failed":
		r.failure, r.message = failureUnauthorized, "Claude Code 拒绝了当前登录，请检查原版 CLI"
	case "billing_error":
		r.failure, r.message = failureQuota, "Claude Code 报告计费或额度错误"
	case "oauth_org_not_allowed", "account_on_hold":
		r.failure, r.message = failureForbidden, "Claude Code 报告当前授权不可用"
	case "rate_limit":
		r.failure, r.message = failureRateLimited, "Claude Code 报告请求限流"
	case "invalid_request":
		r.message = "Claude Code 拒绝了当前请求，请检查模型与思考强度"
	case "model_not_found":
		r.failure, r.message = failureModelSelection, "Claude Code 报告当前模型不可用，请重新选择"
	}
	if subtype == "error_max_structured_output_retries" {
		r.status, r.failure, r.message = "invalid_response", failureResponse, "Claude Code 未返回符合格式的命名决策"
	}
	return r
}
