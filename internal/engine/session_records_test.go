package engine

import (
	"context"
	"testing"
	"time"
)

func TestSessionRecordsUseVerifiedRenameTimeAndStableIdentity(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	ctx := t.Context()
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO sessions(agent,session_id,revision,title,manual,protection_reason,session_kind) VALUES
		('codex','project','check','Same',0,'','project'),('codex','bot','renamed','Same',1,'manual_locked','bot'),
		('codex','failed','check','',0,'','');
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,finalized) VALUES
		('p1','codex','project','first','','Same','applied','','',10,400,100),
		('p2','codex','project','check','Same','Same','kept','','',300,500,300),
		('p3','codex','project','same','Same','Same','applied','','',302,500,500),
		('b1','codex','bot','first','','Same','applied','','',10,200,200),
		('b2','codex','bot','renamed','Other','Same','manual_change','','',220,900,220),
		('f1','codex','failed','check','','','failed','','',600,700,600);`)
	if err != nil {
		t.Fatal(err)
	}
	list := func(filter string, limit int, before *recordCursor) recordList {
		t.Helper()
		return call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: filter, Limit: limit, Before: before})
	}
	page := list("changes", 1, nil)
	if len(page.Items) != 1 || page.Items[0].SessionID != "bot" || page.Next == nil || !page.Items[0].Locked ||
		page.Items[0].Kind != "bot" || page.Items[0].ChangedAt != formatTime(200, time.Local) ||
		len(page.Items[0].Changes) != 2 || page.Items[0].Changes[0].Source != "manual" || !page.Items[0].Changes[0].Found {
		t.Fatalf("invalid verified history: %+v", page)
	}
	next := list("changes", 1, page.Next)
	if len(next.Items) != 1 || next.Items[0].SessionID != "project" || next.Items[0].SessionKey == page.Items[0].SessionKey ||
		next.Items[0].Title != page.Items[0].Title || len(next.Items[0].Changes) != 1 || next.Next != nil {
		t.Fatalf("same-title sessions merged or checks affected history: %+v", next)
	}
	all := list("all", 10, nil)
	if len(all.Items) != 3 || all.Counts.Total != 3 || all.Items[0].SessionID != "failed" || all.Items[1].SessionID != "project" {
		t.Fatalf("all checks are not grouped and sorted by check time: %+v", all)
	}
	locked := list("locked", 10, nil)
	attention := list("attention", 10, nil)
	if len(locked.Items) != 1 || locked.Items[0].SessionID != "bot" ||
		len(attention.Items) != 1 || attention.Items[0].SessionID != "failed" {
		t.Fatalf("incorrect locked or pending filters: %+v %+v", locked, attention)
	}
}

func TestManualLockAndReleaseFencePendingTurns(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir, nil)
	post := func(p submission) {
		t.Helper()
		if got := call[accepted](t, e, "event.submit", p); !got.Accepted {
			t.Fatalf("submission refused: %+v", got)
		}
	}
	post(submission{Agent: "codex", SessionID: "session", Revision: "first", CurrentTitle: "A", Turns: []turn{}})
	post(submission{Agent: "codex", SessionID: "session", Revision: "lock", CurrentTitle: "B", TitleOrigin: "manual", Turns: []turn{}})
	post(submission{Agent: "codex", SessionID: "session", Revision: "skip", CurrentTitle: "C", Turns: []turn{}})
	orca := "old Orca title"
	post(submission{Agent: "codex", SessionID: "session", Revision: "later-manual", CurrentTitle: "C", TitleOrigin: "manual",
		NativeOnly: true, OrcaID: "linked", OrcaTitle: &orca, Turns: []turn{}})
	post(submission{Agent: "codex", SessionID: "session", Revision: "repeat-manual", CurrentTitle: "C", TitleOrigin: "manual",
		NativeOnly: true, OrcaID: "linked", OrcaTitle: &orca, Turns: []turn{}})
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = engineAt(t, dir, nil)
	get := func() record {
		t.Helper()
		items := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "all"}).Items
		if len(items) != 1 {
			t.Fatalf("expected one session, got %+v", items)
		}
		return items[0]
	}
	locked := get()
	if !locked.Locked || locked.Title != "C" || len(locked.Changes) != 2 {
		t.Fatalf("lock or confirmed observations lost across restart: %+v", locked)
	}
	target := call[ProtectionTarget](t, e, "records.protectionTarget", idRequest{ID: locked.ID})
	nextOrca := "observed Orca baseline"
	released := call[accepted](t, e, "records.releaseProtection", protectionRelease{ProtectionTarget: target, NativeTitle: "C", OrcaTitle: &nextOrca})
	if !released.Accepted {
		t.Fatal("unlock refused")
	}
	post(submission{Agent: "codex", SessionID: "session", Revision: "old-flight", CurrentTitle: "C", ProtectionEpoch: &target.Epoch, Turns: []turn{}})
	var events, attempts int
	var stored string
	if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM attempts),orca_unlocked_base FROM sessions`).
		Scan(&events, &attempts, &stored); err != nil {
		t.Fatal(err)
	}
	if events != 3 || attempts != 0 || stored != nextOrca || get().Locked {
		t.Fatalf("skip, retry or unlock created work: events=%d attempts=%d baseline=%q", events, attempts, stored)
	}
}

func TestObservedCodexTurnSharesDedupAndKeepsCandidatePending(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		return modelResult{status: "completed", decision: decision{Action: "rename", Title: goodTitle}}
	}
	p := event("completed", "")
	title := "Codex Chat"
	p.OrcaID, p.OrcaTitle, p.OrcaOrigin, p.ReviewOnly = "linked", &title, "auto", true
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, p.Revision).Status == "needs_review" })
	p.ReviewOnly = false
	call[accepted](t, e, "event.submit", p)
	claim := call[claimResponse](t, e, "title.claim", claimRequest{Agent: p.Agent, SessionID: p.SessionID, Revision: p.Revision})
	list := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "attention"})
	if claim.Candidate != nil || len(list.Items) != 1 || len(list.Items[0].Attempts) != 1 ||
		list.Items[0].SuggestedTitle != goodTitle || list.Items[0].Title != "" || len(list.Items[0].Changes) != 0 {
		t.Fatalf("duplicated or applied unsafe work: %+v %+v", claim, list)
	}
}

func TestLateClaimCannotChangeAnAlreadyAppliedTitleBaseline(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	if _, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('codex','settled','finished',?,0);
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,finalized)
		VALUES('applied','codex','settled','finished','',?,'applied','','',1,2,2)`, goodTitle, goodTitle); err != nil {
		t.Fatal(err)
	}
	got := call[claimResponse](t, e, "title.claim", claimRequest{Agent: "codex", SessionID: "settled", Revision: "finished", CurrentTitle: ""})
	state, err := loadSession(t.Context(), e.db, "codex", "settled")
	if err != nil || got.Status != "applied" || got.Candidate != nil || state.title != goodTitle || state.manual {
		t.Fatalf("late claim changed an applied title: %+v %+v %v", got, state, err)
	}
}
