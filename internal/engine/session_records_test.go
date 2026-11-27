package engine

import (
	"context"
	"slices"
	"sync/atomic"
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
		page.Items[0].Kind != "bot" || page.Items[0].ChangedAt != formatTime(220, time.Local) ||
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

func TestGroupedRecordsKeepActionableSyncAndModelHistory(t *testing.T) {
	targets := make(chan SyncTarget, 1)
	e := engineAt(t, t.TempDir(), func(ctx context.Context, id, before, title string) error {
		target, _ := SyncTargetFromContext(ctx)
		targets <- target
		if id != "orca" || before != "previous" || title != "Same" {
			t.Errorf("unexpected sync: %s %s %s", id, before, title)
		}
		return nil
	})
	_, err := e.db.Exec(`
		INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id) VALUES
		('codex','same','keep','Same',0,'orca'),('claude','same','other','Same',0,''),
		('codex','repaired','check','Same',0,'repair'),('codex','replaced','check','Next',0,'replace');
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_base,orca_origin,sync_status,sync_error,created,updated,finalized,decision_reason,native_handle) VALUES
		('duplicate','codex','same','duplicate','','Same','applied','orca','previous','auto','failed','old',5,6,6,'','old'),
		('rename','codex','same','rename','','Same','applied','orca','previous','auto','failed','needs retry',10,11,11,'','correct'),
		('keep','codex','same','keep','Same','Same','kept','orca',NULL,'auto','','',30,30,30,'throttled',''),
		('other','claude','same','other','','Same','applied','',NULL,'','','',25,25,25,'',''),
		('repair-old','codex','repaired','r1','','Same','applied','repair','previous','auto','failed','old',10,11,11,'',''),
		('repair-new','codex','repaired','r2','Same','Same','native','repair','Same','auto','unchanged','',15,16,16,'',''),
		('replace-old','codex','replaced','r1','','Same','applied','replace','previous','auto','failed','old',10,11,11,'',''),
		('replace-new','codex','replaced','r2','Same','Next','applied','replace','Same','auto','synced','',20,21,21,'','');
		INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started,input_tokens,output_tokens,cached_tokens) VALUES
		('rename','codex','api','first','api','standard','completed',10,100,20,6),
		('rename','codex','api','second','api','standard','completed',11,200,30,8),
		('other','claude','api','different','api','standard','completed',25,600,40,0),
		('rename','claude','api','misattributed','api','standard','completed',11,600,40,0);
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("sync", func(t *testing.T) {
		list := func() recordList {
			return call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "attention"})
		}
		page := list()
		if len(page.Items) != 1 || page.Counts.Attention != 1 || page.Counts.Unread != 1 || page.Counts.SyncFailed != 1 {
			t.Fatalf("expected one actionable sync failure, got %+v", page)
		}
		if row := page.Items[0]; row.ID != "keep" || row.SyncRetryID != "rename" || row.SyncStatus != "failed" || row.SyncError != "needs retry" ||
			row.CanRetry == nil || !*row.CanRetry {
			t.Fatalf("wrong synchronization state: %+v", row)
		}
		for _, id := range []string{"duplicate", "repair-old", "replace-old"} {
			if call[accepted](t, e, "sync.retry", idRequest{id}).Accepted {
				t.Fatalf("obsolete sync failure %s is still eligible", id)
			}
		}
		if !call[accepted](t, e, "sync.retry", idRequest{page.Items[0].SyncRetryID}).Accepted {
			t.Fatal("current sync was not queued")
		}
		eventually(t, func() bool {
			var status string
			return e.db.QueryRow("SELECT sync_status FROM events WHERE id='rename'").Scan(&status) == nil && status == "synced"
		})
		if got := <-targets; got != (SyncTarget{"codex", "same", "correct", "Same"}) {
			t.Fatalf("wrong native target: %+v", got)
		}
		after := list()
		if len(after.Items) != 0 || after.Counts.Attention != 0 || after.Counts.Unread != 0 || after.Counts.SyncFailed != 0 {
			t.Fatalf("completed failure is still shown: %+v", after)
		}
		var attempts int
		if err := e.db.QueryRow("SELECT COUNT(*) FROM attempts").Scan(&attempts); err != nil || attempts != 4 {
			t.Fatalf("sync retry changed model history: %d, %v", attempts, err)
		}
	})
	t.Run("attempts", func(t *testing.T) {
		page := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "all", Query: "first"})
		if len(page.Items) != 1 || page.Items[0].ID != "keep" || len(page.Items[0].Attempts) != 2 {
			t.Fatalf("historical attempts not available on the current row: %+v", page)
		}
		attempts := page.Items[0].Attempts
		if attempts[0].Model != "first" || attempts[1].Model != "second" || *attempts[0].InputTokens != 100 ||
			*attempts[1].OutputTokens != 30 || *attempts[1].CachedTokens != 8 {
			t.Fatalf("wrong attempt usage or ordering: %+v", attempts)
		}
	})
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
	unlocked := get()
	if events != 4 || attempts != 0 || stored != nextOrca || unlocked.Locked || unlocked.Title != "C" ||
		len(unlocked.Changes) != 3 || unlocked.Changes[0].Side != "orca" || unlocked.Changes[0].Source != "observed" ||
		unlocked.Changes[0].From != orca || unlocked.Changes[0].Title != nextOrca || unlocked.CheckedAt != locked.CheckedAt {
		t.Fatalf("skip, retry or unlock created work: events=%d attempts=%d baseline=%q", events, attempts, stored)
	}
}

func TestExplicitCodexRenameAfterManagedSyncLocksOnlyWithMatchingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, orcaID, native, orca, priorStatus string
		locked                                  bool
	}{
		{"external rename", "linked", "External", "External", "", true},
		{"manager write", "linked", "Managed", "Managed", "", false},
		{"different sides", "linked", "External", "Other", "", false},
		{"different binding", "unexpected", "External", "External", "", false},
		{"native claim", "linked", "External", "External", "claimed", false},
		{"previous write", "linked", "External", "External", "applied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			_, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id,orca_managed)
				VALUES('codex','native','previous','Managed',0,'linked','Managed')`)
			if err != nil {
				t.Fatal(err)
			}
			if tc.priorStatus != "" {
				_, err = e.db.Exec(`INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated)
					VALUES('prior','codex','native','prior','Managed','External',?,'linked','auto',1,1)`, tc.priorStatus)
				if err != nil {
					t.Fatal(err)
				}
			}
			p := submission{Agent: "codex", SessionID: "native", Revision: "next", CurrentTitle: tc.native, TitleOrigin: "unknown",
				OrcaID: tc.orcaID, OrcaNativeHandle: "native", OrcaTitle: &tc.orca, OrcaOrigin: "manual", Turns: []turn{}}
			call[accepted](t, e, "event.submit", p)
			p.Revision = "following"
			call[accepted](t, e, "event.submit", p)
			var reason string
			var changes, following, attempts int
			err = e.db.QueryRow(`SELECT protection_reason,
				(SELECT count(*) FROM events WHERE status='manual_change'),
				(SELECT count(*) FROM events WHERE revision='following'), (SELECT count(*) FROM attempts) FROM sessions`).
				Scan(&reason, &changes, &following, &attempts)
			if err != nil {
				t.Fatal(err)
			}
			if (reason == decisionManual) != tc.locked || tc.locked && (changes != 1 || following != 0 || attempts != 0) {
				t.Fatalf("unexpected protection: reason=%q changes=%d following=%d attempts=%d", reason, changes, following, attempts)
			}
		})
	}
}

func TestOrcaRecoveryCannotSubmitAnIndependentNamingCandidate(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	reply, err := e.Handle(t.Context(), "event.submit", []byte(`{"agent":"codex","sessionId":"session","revision":"finished","currentTitle":"",
		"orcaSessionId":"linked","orcaTitle":"Codex Chat","orcaTitleOrigin":"auto","reviewOnly":true,
		"turns":[{"role":"user","text":"Check the unrelated candidate"}]}`))
	if err == nil {
		t.Fatalf("Orca-only naming submission accepted: %+v", reply)
	}
	var events, attempts int
	if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM attempts)`).Scan(&events, &attempts); err != nil || events != 0 || attempts != 0 {
		t.Fatalf("rejected submission created work: events=%d attempts=%d err=%v", events, attempts, err)
	}
}

func TestOrcaRecoveryDoesNotRenameOrRepeatAgentWork(t *testing.T) {
	var models, writes atomic.Int32
	e := engineAt(t, t.TempDir(), func(context.Context, string, string, string) error {
		writes.Add(1)
		return nil
	})
	e.generate = func(_ context.Context, _ attemptRequest, start func() error) modelResult {
		models.Add(1)
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		return modelResult{status: "completed", decision: decision{Action: "rename", Title: goodTitle}}
	}
	ctx := t.Context()
	p := event("finished", "")
	find := func() ConfirmedBinding {
		t.Helper()
		v, err := e.UnboundAppliedTitle(ctx, p.Agent, p.SessionID, p.Revision)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{Agent: p.Agent, SessionID: p.SessionID, Revision: p.Revision})
	if c.Candidate == nil || find().ID != "" {
		t.Fatal("a claim must never act as a native write receipt")
	}
	call[accepted](t, e, "title.ack", ackRequest{Agent: p.Agent, SessionID: p.SessionID, ID: c.Candidate.ID, ObservedTitle: goodTitle})
	v := find()
	if v.ID != c.Candidate.ID || v.NativeTitle != goodTitle {
		t.Fatalf("native acknowledgement was unavailable: %+v", v)
	}
	v.OrcaID, v.OrcaTitle, v.Origin, v.ReadbackOnly = "linked", "Manual Orca title", "manual", true
	for range 2 {
		if err := e.AttachBinding(ctx, v); err != nil {
			t.Fatal(err)
		}
		call[accepted](t, e, "event.submit", p)
	}
	current := latest(t, e)
	if find().ID != "" || current.SyncStatus != "blocked" || current.Title != goodTitle || models.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("recovery created new naming work: %+v, models=%d, writes=%d", current, models.Load(), writes.Load())
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

func TestChangeOrderUsesEvidenceAndKeepsEqualTimePagesStable(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	if _, err := e.db.Exec(`
		INSERT INTO sessions(agent,session_id,revision,title,manual,protection_reason) VALUES
			('codex','mixed','skipped2','new',1,'manual_locked'),('codex','auto','r','new',0,''),
			('codex','manual','r','new',1,'manual_locked'),('codex','missing','r','new',0,''),
			('codex','old-migration','r','new',0,''),('codex','verified','r','new',1,'manual_locked');
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated,finalized,renamed_at) VALUES
			('old','codex','mixed','old','','base','applied','','',10,20,20,NULL),
			('manual','codex','mixed','edit','base','new','manual_change','','',60,60,60,NULL),
			('skip1','codex','mixed','skipped','new','new','kept','','',100,999,100,NULL),
			('skip2','codex','mixed','skipped2','new','new','kept','','',200,999,200,NULL),
			('auto','codex','auto','r','','new','applied','','',30,55,55,NULL),
			('only-manual','codex','manual','r','before','new','manual_change','','',60,900,60,NULL),
			('missing','codex','missing','r','','new','applied','','',900,900,NULL,NULL),
			('old-migration','codex','old-migration','r','','new','applied','','',800,800,799,NULL),
			('verified','codex','verified','r','before','new','manual_change','','',92,92,92,42);`); err != nil {
		t.Fatal(err)
	}
	list := func(filter string, limit int, before *recordCursor) recordList {
		t.Helper()
		return call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: filter, Limit: limit, Before: before})
	}
	var order []string
	var cursor *recordCursor
	for {
		page := list("changes", 1, cursor)
		if len(page.Items) != 1 || len(order) > 6 {
			t.Fatalf("unstable pagination: %+v", page)
		}
		item := page.Items[0]
		order = append(order, item.SessionID)
		first := item.Changes[0]
		if item.ChangedAt != first.At || item.ChangedFound != first.Found {
			t.Fatalf("summary and history disagree: %+v", item)
		}
		switch item.SessionID {
		case "verified":
			if first.Found || first.At != formatTime(42, time.Local) {
				t.Fatalf("verified rename time was replaced by readback time: %+v", first)
			}
		case "old-migration", "missing":
			if first.At != "" {
				t.Fatalf("inferred a timestamp for legacy change: %+v", first)
			}
		}
		cursor = page.Next
		if cursor == nil {
			break
		}
	}
	if !slices.Equal(order, []string{"manual", "mixed", "auto", "verified", "old-migration", "missing"}) {
		t.Fatalf("unexpected change order: %v", order)
	}
	all := list("all", 10, nil)
	if all.Items[0].SessionID != "missing" || all.Items[2].SessionID != "mixed" ||
		all.Items[2].CheckedAt != formatTime(200, time.Local) {
		t.Fatalf("all should sort by checks, not retries or renames: %+v", all.Items)
	}
}

func TestUnlockRecordsOnlyObservedTitleDifference(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	call[accepted](t, e, "event.submit", submission{Agent: "codex", SessionID: "locked", Revision: "known",
		CurrentTitle: "old name", TitleOrigin: "manual", Turns: []turn{}})
	locked := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "locked"}).Items[0]
	snapshot := call[ProtectionTarget](t, e, "records.protectionTarget", idRequest{locked.ID})
	change := protectionRelease{ProtectionTarget: snapshot, NativeTitle: "different native name"}
	if !call[accepted](t, e, "records.releaseProtection", change).Accepted ||
		call[accepted](t, e, "records.releaseProtection", change).Accepted {
		t.Fatal("unlock did not enforce the observed baseline")
	}
	got := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "all"}).Items[0]
	if got.Locked || got.Title != change.NativeTitle || got.CheckedAt != locked.CheckedAt || len(got.Changes) != 1 ||
		got.Changes[0].Source != "observed" || !got.Changes[0].Found || got.Changes[0].From != "old name" {
		t.Fatalf("readback attribution or check time is incorrect: %+v", got)
	}
}

func TestAttentionHidesFailureSupersededByLatestCheck(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	if _, err := e.db.Exec(`INSERT INTO sessions(agent,session_id,revision,title,manual) VALUES('codex','old','latest','existing',0);
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated)
		VALUES('failed','codex','old','first','','','failed','','',10,900),
		('latest','codex','old','latest','existing','existing','kept','','',20,20);`); err != nil {
		t.Fatal(err)
	}
	pending := call[recordList](t, e, "records.list", listRequest{Grouped: true, Filter: "attention"})
	if len(pending.Items) != 0 || pending.Counts.Attention != 0 || pending.Counts.Unread != 0 {
		t.Fatalf("stale failure remains actionable: %+v", pending)
	}
}
