package engine

import (
	"context"
	"errors"
	"net/http"
)

type failureReason struct {
	Code   failureCode `json:"code"`
	Length *int        `json:"length,omitempty"`
	Limit  *int        `json:"limit,omitempty"`
}

type failureCode string

const (
	failureTooLong        failureCode = "title_too_long"
	failureCategory       failureCode = "title_category"
	failureSeparator      failureCode = "title_separator"
	failureTitleFormat    failureCode = "title_format"
	failureResponse       failureCode = "invalid_response"
	failureCodexLogin     failureCode = "codex_login"
	failureClaudeLogin    failureCode = "claude_login"
	failureModelSelection failureCode = "model_selection"
	failureUnauthorized   failureCode = "unauthorized"
	failureForbidden      failureCode = "forbidden"
	failureRateLimited    failureCode = "rate_limited"
	failureQuota          failureCode = "quota_exhausted"
	failureTimeout        failureCode = "timeout"
	failureCanceled       failureCode = "canceled"
	failureRequest        failureCode = "request_failed"
	failureTool           failureCode = "tool_rejected"
	failureGeneric        failureCode = "naming_failed"
	failureLegacyTitle    failureCode = "legacy_title_validation"
	failureUnrecorded     failureCode = "unrecorded"
)

func requestFailure(status int) failureCode {
	switch status {
	case http.StatusUnauthorized:
		return failureUnauthorized
	case http.StatusForbidden:
		return failureForbidden
	case http.StatusTooManyRequests:
		return failureRateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return failureTimeout
	default:
		return failureRequest
	}
}

func contextFailure(err error) failureCode {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return failureTimeout
	case errors.Is(err, context.Canceled):
		return failureCanceled
	default:
		return ""
	}
}
