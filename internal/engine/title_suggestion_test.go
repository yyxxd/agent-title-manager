package engine

import (
	"context"
	"strings"
	"testing"
)

func TestTitleSuggestionKeepsHistoryAndCoalescesConcurrentClicks(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	_, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual,protection_reason,protection_epoch)
		VALUES('codex','native','prior','Manual',1,'manual_locked',7);
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,finalized)
		VALUES('record','codex','native','prior','','Manual','manual_change','','',100,100,100)`)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	e.generate = func(_ context.Context, req attemptRequest, start func() error) modelResult {
		if !req.suggestion || !strings.Contains(string(req.payload), `"title_suggestion":true`) ||
			!strings.Contains(string(req.payload), "检查分针") {
			t.Errorf("incorrect suggestion payload: %s", req.payload)
		}
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{}
		}
		close(started)
		<-release
		return modelResult{status: "completed", decision: decision{Action: "rename", Title: "🧩 时钟｜校验分针"}}
	}
	read := func(_ context.Context, target SuggestionTarget) (SuggestionObservation, error) {
		return SuggestionObservation{NamingSnapshot: NamingSnapshot{SessionID: target.SessionID, Revision: "completed",
			CurrentTitle: "Manual", TitleOrigin: "unknown", Turns: []NamingTurn{{Role: "user", Text: "检查分针"}}}}, nil
	}
	before := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "changes", Limit: 10})
	done := make(chan TitleSuggestion, 1)
	go func() {
		result, err := e.SuggestTitle(t.Context(), "record", read)
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	<-started
	duplicate, duplicateErr := e.SuggestTitle(t.Context(), "record", read)
	close(release)
	result := <-done
	if duplicateErr == nil || duplicate.Title != "" || result.Title != "🧩 时钟｜校验分针" {
		t.Fatalf("unexpected suggestion: %+v, duplicate=%+v %v", result, duplicate, duplicateErr)
	}
	after := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "changes", Limit: 10})
	var attempts, epoch, records int
	err = e.db.QueryRow(`SELECT protection_epoch,
		(SELECT count(*) FROM attempts WHERE suggestion=1 AND agent='codex'),(SELECT count(*) FROM events) FROM sessions`).
		Scan(&epoch, &attempts, &records)
	if err != nil || attempts != 1 || epoch != 7 || records != 1 || len(after.TestAttempts) != 0 || len(after.Items) != 1 ||
		!after.Items[0].Locked || after.Items[0].Title != "Manual" || before.Items[0].ChangedAt != after.Items[0].ChangedAt ||
		len(after.Items[0].Changes) != 1 {
		t.Fatalf("suggestion modified history: %+v epoch=%d attempts=%d events=%d err=%v", after, epoch, attempts, records, err)
	}
}

func TestTitleSuggestionStopsOnMissingContentAndChangedTitle(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	_, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('codex','native','prior','Original',0);
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated)
		VALUES('record','codex','native','prior','','Original','kept','','',100,100)`)
	if err != nil {
		t.Fatal(err)
	}
	current := NamingSnapshot{SessionID: "native", CurrentTitle: "Original", Revision: "completed"}
	read := func(context.Context, SuggestionTarget) (SuggestionObservation, error) {
		return SuggestionObservation{NamingSnapshot: current}, nil
	}
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Fatal(err)
		}
		current.CurrentTitle = "Changed"
		return modelResult{status: "completed", decision: decision{Action: "rename", Title: "🧩 时钟｜校验分针"}}
	}
	if _, err = e.SuggestTitle(t.Context(), "record", read); err == nil || !strings.Contains(err.Error(), "会话文字") {
		t.Fatalf("unexpected empty content result: %v", err)
	}
	current.Turns = []NamingTurn{{Role: "user", Text: "检查分针"}}
	result, err := e.SuggestTitle(t.Context(), "record", read)
	if err != errSuggestionChanged || result.Title != "" {
		t.Fatalf("stale suggestion returned: %+v, %v", result, err)
	}
	var attempts int
	if err := e.db.QueryRow("SELECT count(*) FROM attempts").Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("unexpected attempts %d err=%v", attempts, err)
	}
}
