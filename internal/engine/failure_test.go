package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamingFailureRecords(t *testing.T) {
	const private = "private-fixture"
	for _, tc := range []struct {
		name, title string
		httpStatus  int
		code        failureCode
		length      int
	}{
		{"length", "⚙️ 构建｜" + private, 200, failureTooLong, 21},
		{"category", "构建｜修复", 200, failureCategory, 0},
		{"separator", "🧩 构建|修复", 200, failureSeparator, 0},
		{"format", "🧩 构建｜修@复", 200, failureTitleFormat, 0},
		{"json", "", 200, failureResponse, 0},
		{"unauthorized", "", 401, failureUnauthorized, 0},
		{"forbidden", "", 403, failureForbidden, 0},
		{"rate limited", "", 429, failureRateLimited, 0},
		{"gateway timeout", "", 504, failureTimeout, 0},
		{"unclassified request", "", 502, failureRequest, 0},
		{"success", goodTitle, 200, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			e := engineAt(t, dir, nil)
			httpProvider(t, e, failureReply(t, tc.title, tc.httpStatus, private))
			p := event("r", "原有自动标题")
			p.TitleOrigin = "auto"
			call[accepted](t, e, "event.submit", p)
			status := "failed"
			if tc.code == "" {
				status = "candidate"
			}
			eventually(t, func() bool { return latest(t, e).Status == status })
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := engineAt(t, dir, nil)
			call[Settings](t, reopened, "settings.save", map[string]int{"maxTitleLength": 30})
			r := latest(t, reopened)
			checkFailureRecord(t, r, p.CurrentTitle, private, tc.code, tc.length)
		})
	}
}

func failureReply(t *testing.T, title string, status int, private string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(private))
			return
		}
		content := private
		if title != "" {
			data, err := json.Marshal(decision{Action: "rename", Title: title, Reason: private})
			if err != nil {
				t.Error(err)
				return
			}
			content = string(data)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": chatMessage{Content: content}}},
			"usage":   map[string]int{"prompt_tokens": 17, "completion_tokens": 0},
		}); err != nil {
			t.Error(err)
		}
	}
}

func checkFailureRecord(t *testing.T, r record, original, private string, code failureCode, length int) {
	t.Helper()
	attempts := 1
	switch code {
	case failureTooLong, failureCategory, failureSeparator, failureTitleFormat:
		attempts = 2
	}
	if len(r.Attempts) != attempts || r.BaseTitle != original {
		t.Fatal(r)
	}
	if code == "" {
		if r.Failure != nil || r.Title != goodTitle || r.Status != "candidate" {
			t.Fatal(r)
		}
		return
	}
	if r.Status != "failed" || r.Failure == nil || r.Failure.Code != code || r.Title != original {
		t.Fatal(r)
	}
	if length == 0 {
		if r.Failure.Length != nil || r.Failure.Limit != nil {
			t.Fatal(r.Failure)
		}
	} else if r.Failure.Length == nil || *r.Failure.Length != length ||
		r.Failure.Limit == nil || *r.Failure.Limit != 15 {
		t.Fatal(r.Failure)
	}
	if code == failureTooLong || code == failureResponse {
		a := r.Attempts[0]
		if a.InputTokens == nil || *a.InputTokens != 17 || a.OutputTokens == nil || *a.OutputTokens != 0 || a.CachedTokens != nil {
			t.Fatal(a)
		}
	}
	if data, err := json.Marshal(r); err != nil || strings.Contains(string(data), private) {
		t.Fatalf("记录包含原始响应内容或无法序列化: %v", err)
	}
}

func TestLegacyFailureRecordsAfterMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	const seed = `
INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('pi','s','app','原名',0);
INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,sync_status,created,updated) VALUES
	('validated','pi','s','r1','原名','原名','failed','','','',1,1),
	('fallback','pi','s','r2','原名','原名','failed','','','',1,2),
	('rejected','pi','s','r3','原名','原名','failed','','','',1,3),
	('applied','pi','s','app','原名','新名','applied','','','failed',1,4),
	('interrupted','pi','s','r4','原名','原名','running','','','',1,5);
INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started,input_tokens) VALUES
	('validated','pi','codex','fixture','codex_subscription','default','failed',1,NULL),
	('validated','pi','api','fixture','openai_compatible','default','completed',1,8),
	('fallback','pi','codex','fixture','codex_subscription','default','completed',1,9),
	('fallback','pi','api','fixture','openai_compatible','default','failed',1,NULL),
	('rejected','pi','api','fixture','openai_compatible','default','invalid_response',1,10),
	('applied','pi','api','fixture','openai_compatible','default','completed',1,11),
	('interrupted','pi','api','fixture','openai_compatible','default','running',1,NULL);
`
	for _, query := range []string{schema, schemaV2, schemaV3, seed} {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir, nil)
	var version int
	if err := e.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatal(version, err)
	}
	checkLegacyFailureRecords(t, call[recordList](t, e, "records.list", listRequest{}).Items)
}

func checkLegacyFailureRecords(t *testing.T, items []record) {
	t.Helper()
	want := map[string]failureCode{
		"validated": failureLegacyTitle, "fallback": failureUnrecorded, "rejected": failureUnrecorded,
		"applied": "", "interrupted": "",
	}
	if len(items) != len(want) {
		t.Fatal(items)
	}
	for _, r := range items {
		code, ok := want[r.ID]
		if !ok || code == "" && r.Failure != nil ||
			code != "" && (r.Failure == nil || r.Failure.Code != code || r.Failure.Length != nil || r.Failure.Limit != nil) {
			t.Fatal(r)
		}
		switch r.ID {
		case "validated":
			if len(r.Attempts) != 2 {
				t.Fatal(r.Attempts)
			}
			a := r.Attempts[1]
			if a.Status != "completed" || a.InputTokens == nil || *a.InputTokens != 8 {
				t.Fatal(r.Attempts)
			}
		case "applied":
			if r.SyncStatus != "failed" || r.Title != "新名" {
				t.Fatal(r)
			}
		case "interrupted":
			if r.Status != "interrupted" || len(r.Attempts) != 1 || r.Attempts[0].Status != "interrupted" {
				t.Fatal(r)
			}
		}
	}
}

func TestNamingFailurePreservesInterruptedOutcome(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want failureCode
	}{{context.DeadlineExceeded, failureTimeout}, {context.Canceled, failureCanceled}, {nil, ""}} {
		result := modelResult{status: "interrupted", failure: contextFailure(tc.err)}
		title, status, finalized, reason := modelOutcome("原名", true, result, 15, 0)
		if title != "原名" || status != "interrupted" || finalized != nil || reason.Code != tc.want {
			t.Fatal(title, status, finalized, reason)
		}
	}
}

func TestAPIFailureUsesRequestInterruptionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		want failureCode
	}{{"deadline before response", failureTimeout}, {"canceled after headers", failureCanceled}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				if tc.want == failureCanceled {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					cancel()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			r := runAPI(ctx, Settings{APIBaseURL: server.URL, APIProtocol: protocolChat, APIModel: "fixture"},
				nil, "fixture", []byte("{}"), func() error { return nil })
			if r.status != "interrupted" || r.failure != tc.want || r.confirmed {
				t.Fatal(r)
			}
		})
	}
}
