package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestNamingCorrectionRetriesOnceWithStructuredFeedback(t *testing.T) {
	category, _, _ := strings.Cut(goodTitle, " ")
	longTitle := category + " 修改记录｜" + strings.Repeat("长", 9)
	for _, tc := range []struct {
		name, first, second string
		code                failureCode
	}{
		{"length corrected", longTitle, goodTitle, failureTooLong},
		{"length still invalid", longTitle, longTitle, failureTooLong},
		{"separator corrected", category + " 项目 | 修复", goodTitle, failureSeparator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			var calls atomic.Int32
			httpProvider(t, e, func(w http.ResponseWriter, r *http.Request) {
				var req chatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 {
					t.Errorf("invalid request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var prompt struct {
					Feedback *titleRetry `json:"retry_feedback"`
					Current  string      `json:"current_title"`
				}
				if err := json.Unmarshal([]byte(req.Messages[1].Content), &prompt); err != nil {
					t.Error(err)
				}
				title := tc.first
				switch calls.Add(1) {
				case 1:
					if prompt.Feedback != nil {
						t.Error("feedback was sent on the first attempt")
					}
				case 2:
					title = tc.second
					want := titleRetry{tc.first, tc.code, utf8.RuneCountInString(tc.first), 15}
					if prompt.Feedback == nil || *prompt.Feedback != want || prompt.Current != "" {
						t.Errorf("feedback: %+v; want %+v", prompt.Feedback, want)
					}
					policy := req.Messages[0].Content
					if !strings.Contains(policy, "仍用 rename") || !strings.Contains(policy, "全角｜") {
						t.Error("correction policy missing")
					}
				default:
					t.Error("correction exceeded one retry")
				}
				failureReply(t, title, http.StatusOK, "fixture")(w, r)
			})
			call[accepted](t, e, "event.submit", event("r", ""))
			wantStatus := "candidate"
			if tc.first == tc.second {
				wantStatus = "failed"
			}
			eventually(t, func() bool { return latest(t, e).Status == wantStatus })
			result := latest(t, e)
			if calls.Load() != 2 || len(result.Attempts) != 2 {
				t.Fatalf("calls=%d, recorded attempts=%d", calls.Load(), len(result.Attempts))
			}
			for _, a := range result.Attempts {
				if a.Provider != "api" || a.Status != "completed" || a.InputTokens == nil || *a.InputTokens != 17 {
					t.Fatalf("wrong attempt usage or provider: %+v", a)
				}
			}
			if wantStatus == "failed" && (result.Failure == nil || result.Failure.Code != failureTooLong) {
				t.Fatalf("final failure: %+v", result.Failure)
			}
		})
	}
}

func TestDisabledWhileModelReturnsInvalidTitleStopsCorrection(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	e, err := Open(t.TempDir(), nil, func(context.Context, string) bool { return enabled.Load() })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	e.generate = func(_ context.Context, req attemptRequest, start func() error) modelResult {
		if req.feedback != nil {
			t.Error("disabled correction started")
		}
		if err := start(); err != nil {
			t.Error(err)
		}
		enabled.Store(false)
		return modelResult{status: "completed", decision: decision{Action: "rename", Title: "invalid"}}
	}
	call[accepted](t, e, "event.submit", event("r", ""))
	eventually(t, func() bool { return latest(t, e).Status == "disabled" })
	if got := len(latest(t, e).Attempts); got != 1 {
		t.Fatalf("recorded %d attempts", got)
	}
}
