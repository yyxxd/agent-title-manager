package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNamingPresetOutbound(t *testing.T) {
	for _, tc := range []struct {
		limit, low, high int
		band             string
		examples         []string
	}{
		{15, 10, 12, "10–12", []string{"🧩 会话标题｜自动命名", "🧩 UI｜Fix nav"}},
		{30, 18, 24, "18–24", []string{"🧩 会话标题｜自动命名并同步到 Orca", "🧩 CLI｜Fix startup flow"}},
	} {
		t.Run(fmt.Sprint(tc.limit), func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			requests := make(chan chatRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chatRequest
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("unexpected API request")
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				requests <- request
				failureReply(t, tc.examples[0], http.StatusOK, "")(w, r)
			}))
			defer server.Close()
			settings := call[Settings](t, e, "settings.save", map[string]any{
				"provider": "api", "apiBaseUrl": server.URL, "apiModel": "fixture", "maxTitleLength": tc.limit,
			})
			payload, err := makePrompt(event("1", ""))
			if err != nil {
				t.Fatal(err)
			}
			result := e.runProvider(context.Background(), attemptRequest{provider: "api", settings: settings, payload: payload}, func() error { return nil })
			if result.status != "completed" || result.decision.Title != tc.examples[0] || len(requests) != 1 {
				t.Fatal(result, len(requests))
			}
			checkPresetRequest(t, <-requests, settings, payload, tc.band, tc.examples)
			for _, example := range tc.examples {
				length := utf8.RuneCountInString(example)
				if reason := titleFailure(example, tc.limit); reason.Code != "" || length < tc.low || length > tc.high {
					t.Fatalf("invalid preset example %q (%d): %+v", example, length, reason)
				}
			}
		})
	}
}

func checkPresetRequest(t *testing.T, request chatRequest, settings Settings, payload []byte, band string, examples []string) {
	t.Helper()
	policy, input, err := providerPrompt(payload, settings.MaxTitleLength)
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "fixture" || len(request.Messages) != 2 ||
		request.Messages[0] != (chatMessage{"system", policy}) || request.Messages[1] != (chatMessage{"user", string(input)}) {
		t.Fatal("API request does not contain the exact assembled policy and JSON input")
	}
	var data struct {
		MaxTitleLength int    `json:"max_title_length"`
		Turns          []turn `json:"recent_turns"`
	}
	if err := json.Unmarshal(input, &data); err != nil || data.MaxTitleLength != settings.MaxTitleLength || len(data.Turns) == 0 {
		t.Fatal(data, err)
	}
	otherBand := "18–24"
	if band == otherBand {
		otherBand = "10–12"
	}
	for _, expected := range append([]string{fmt.Sprintf("最多 %d", settings.MaxTitleLength), band}, examples...) {
		if !strings.Contains(policy, expected) {
			t.Fatalf("missing policy text %q", expected)
		}
	}
	if strings.Contains(policy, "conflicting_titles") || strings.Contains(policy, otherBand) {
		t.Fatal("policy contains irrelevant input or another preset")
	}
	dir := t.TempDir()
	if _, err := codexArgs(dir, settings, policy); err != nil {
		t.Fatal(err)
	}
	codexPolicy, err := os.ReadFile(filepath.Join(dir, "naming.md"))
	if err != nil || string(codexPolicy) != policy {
		t.Fatal("Codex policy differs from API", err)
	}
	schema, err := os.ReadFile(filepath.Join(dir, "schema.json"))
	if err != nil || string(schema) != decisionSchema {
		t.Fatal("Codex structured response schema changed", err)
	}
}

func TestTitlePresetValidationBoundaries(t *testing.T) {
	const prefix = "⚙️ 代理｜"
	for _, limit := range []int{shortTitleLength, detailedTitleLength} {
		title := prefix + strings.Repeat("改", limit-utf8.RuneCountInString(prefix))
		for _, delta := range []int{0, 1} {
			result := modelResult{status: "completed", decision: decision{Action: "rename", Title: title + strings.Repeat("改", delta)}}
			_, status, _, reason := modelOutcome("原名", true, result, limit, time.Now().UnixMilli())
			if delta == 0 && (status != "candidate" || reason.Code != "") {
				t.Fatal(limit, status, reason)
			}
			if delta == 1 && (status != "failed" || reason.Code != failureTooLong || *reason.Length != limit+1 || *reason.Limit != limit) {
				t.Fatal(limit, status, reason)
			}
		}
		original := prefix + strings.Repeat("留", 35)
		result := modelResult{status: "completed", decision: decision{Action: "keep", Title: original}}
		if title, status, _, reason := modelOutcome(original, true, result, limit, 0); status != "kept" || title != original || reason.Code != "" {
			t.Fatal("an existing title must survive a keep decision", status, reason)
		}
	}
}

func TestTitlePresetSave(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	if s := call[Settings](t, e, "settings.get", nil); s.MaxTitleLength != 15 {
		t.Fatal(s)
	}
	call[Settings](t, e, "settings.save", map[string]int{"maxTitleLength": 30})
	for _, input := range []string{`22`, `23`, `48`, `0`, `-1`, `null`, `"30"`, `15.5`, `true`} {
		request := json.RawMessage(`{"maxTitleLength":` + input + `,"fast":false}`)
		if _, err := e.Handle(context.Background(), "settings.save", request); err == nil {
			t.Fatalf("saved non-preset %s", input)
		}
	}
	if s := call[Settings](t, e, "settings.get", nil); s.MaxTitleLength != 30 || !s.Fast {
		t.Fatal("invalid save changed persisted settings", s)
	}
	if s := call[Settings](t, e, "settings.save", map[string]bool{"fast": false}); s.MaxTitleLength != 30 {
		t.Fatal("unrelated save changed the preset", s)
	}
}

func TestTitlePresetMigration(t *testing.T) {
	for _, tc := range []struct{ old, new int }{{8, 15}, {15, 15}, {22, 15}, {23, 30}, {30, 30}, {48, 30}} {
		t.Run(fmt.Sprint(tc.old), func(t *testing.T) {
			dir := t.TempDir()
			seedLegacyTitlePreset(t, dir, tc.old)
			e := engineAt(t, dir, nil)
			s := call[Settings](t, e, "settings.get", nil)
			if s.MaxTitleLength != tc.new || s.CodexModel != "fixture" || s.Fast {
				t.Fatal(s)
			}
			var version int
			if err := e.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
				t.Fatal(version, err)
			}
			r := latest(t, e)
			if r.Failure == nil || r.Failure.Code != failureTooLong || *r.Failure.Length != 28 || *r.Failure.Limit != 22 ||
				len(r.Attempts) != 1 || r.Attempts[0].InputTokens == nil || *r.Attempts[0].InputTokens != 13 {
				t.Fatal("migration changed historical evidence", r)
			}
		})
	}
}

func seedLegacyTitlePreset(t *testing.T, dir string, length int) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const history = `
INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('pi','fixture','r','原名',0);
INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,failure_code,failure_length,failure_limit)
VALUES('past','pi','fixture','r','原名','原名','failed','','',1,2,'title_too_long',28,22);
INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started,input_tokens)
VALUES('past','pi','api','fixture','openai_compatible','default','completed',1,13);
`
	for _, query := range []string{schema, schemaV2, schemaV3, schemaV4, history} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE settings SET max_length=?,codex_model='fixture',fast=0", length); err != nil {
		t.Fatal(err)
	}
}
