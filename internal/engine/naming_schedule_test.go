package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestInitialNamingSurvivesFollowUpsAndUsesLatestClaimRevision(t *testing.T) {
	for _, phase := range []string{"running", "candidate", "claimed"} {
		t.Run(phase, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			started, finish := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			e.generate = func(ctx context.Context, _ attemptRequest, start func() error) modelResult {
				if err := start(); err != nil {
					t.Error(err)
					return modelResult{status: "failed"}
				}
				if requests.Add(1) == 1 && phase == "running" {
					close(started)
					select {
					case <-finish:
					case <-ctx.Done():
						t.Error("first unnamed job was canceled")
						return modelResult{status: "interrupted"}
					}
				}
				return modelResult{status: "completed", decision: decision{Action: "rename", Title: goodTitle}}
			}
			p := event("first", "")
			call[accepted](t, e, "event.submit", p)
			switch phase {
			case "running":
				waitNamingSignal(t, started)
			case "candidate", "claimed":
				eventually(t, func() bool { return latest(t, e).Status == "candidate" })
				if phase == "claimed" && call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, "", p.Revision}).Candidate == nil {
					t.Fatal("initial claim unavailable")
				}
			}
			for _, revision := range []string{"follow-up", "latest"} {
				p.Revision = revision
				p.Turns = append(p.Turns, turn{"user", revision}, turn{"assistant", "done"})
				call[accepted](t, e, "event.submit", p)
			}
			req := claimRequest{p.Agent, p.SessionID, "", p.Revision}
			if phase == "running" {
				if got := call[claimResponse](t, e, "title.claim", req); got.Status != "running" || got.Candidate != nil {
					t.Fatalf("latest did not wait for first model request: %+v", got)
				}
				close(finish)
			}
			var candidate *candidate
			eventually(t, func() bool {
				candidate = call[claimResponse](t, e, "title.claim", req).Candidate
				return candidate != nil
			})
			first := revisionRecord(t, e, "first")
			if candidate.ID != first.ID || candidate.Revision != "latest" || candidate.BaseTitle != "" {
				t.Fatalf("wrong claim: %+v", candidate)
			}
			reply := call[accepted](t, e, "title.ack", ackRequest{p.Agent, p.SessionID, candidate.ID, candidate.Title})
			eventRevision, sessionRevision, unbound := UnboundAckRevisions(reply)
			if !unbound || eventRevision != "first" || sessionRevision != "latest" {
				t.Fatalf("ack: %+v", reply)
			}
			if err := e.AttachBinding(t.Context(), ConfirmedBinding{
				ID: candidate.ID, Agent: p.Agent, SessionID: p.SessionID, Revision: eventRevision,
				SessionRevision: sessionRevision, NativeTitle: candidate.Title, OrcaID: "chat", Origin: "auto",
			}); err != nil || revisionRecord(t, e, "first").SyncStatus != "pending" {
				t.Fatalf("latest-revision ack did not attach to the original event: %v", err)
			}
			for _, r := range []string{"follow-up", "latest"} {
				rec := revisionRecord(t, e, r)
				if rec.Status != "stale" || len(rec.Attempts) != 0 {
					t.Fatalf("queued duplicate %s: %+v", r, rec)
				}
			}
			if requests.Load() != 1 || revisionRecord(t, e, "first").Status != "applied" {
				t.Fatal("initial title not applied once")
			}
		})
	}
}

func TestManualObservationCancelsInitialAttemptBeforeCorrection(t *testing.T) {
	for _, via := range []string{"event.submit", "title.claim"} {
		t.Run(via, func(t *testing.T) {
			e := engineAt(t, t.TempDir(), nil)
			started, canceled := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			e.generate = func(ctx context.Context, _ attemptRequest, start func() error) modelResult {
				if err := start(); err != nil {
					t.Error(err)
				}
				if calls.Add(1) != 1 {
					t.Error("unexpected correction")
					return modelResult{status: "failed"}
				}
				close(started)
				<-ctx.Done()
				close(canceled)
				return modelResult{status: "completed", decision: decision{Action: "rename", Title: "invalid"}}
			}
			p := event("first", "")
			call[accepted](t, e, "event.submit", p)
			waitNamingSignal(t, started)
			const manual = "用户手动标题"
			if via == "event.submit" {
				p.Revision, p.CurrentTitle, p.TitleOrigin = "manual", manual, "manual"
				call[accepted](t, e, "event.submit", p)
			} else {
				r := call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, manual, p.Revision})
				if r.Status != "stale" {
					t.Fatalf("manual title was not observed: %+v", r)
				}
			}
			waitNamingSignal(t, canceled)
			eventually(t, func() bool {
				rec := revisionRecord(t, e, "first")
				return rec.Status == "stale" && len(rec.Attempts) == 1 && rec.Attempts[0].Status == "completed"
			})
			s, err := loadSession(t.Context(), e.db, p.Agent, p.SessionID)
			if err != nil || !s.manual || s.title != manual || calls.Load() != 1 {
				t.Fatalf("manual title lost: %+v, %v, requests=%d", s, err, calls.Load())
			}
		})
	}
}

func TestNewTurnCancelsAlreadyNamedInFlightRequest(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	started, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	e.generate = func(ctx context.Context, _ attemptRequest, start func() error) modelResult {
		if err := start(); err != nil {
			t.Error(err)
			return modelResult{status: "failed"}
		}
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			return modelResult{status: "interrupted"}
		}
		return modelResult{status: "completed", decision: decision{Action: "keep"}}
	}
	p := event("old", goodTitle)
	p.TitleOrigin = "auto"
	call[accepted](t, e, "event.submit", p)
	waitNamingSignal(t, started)
	p.Revision = "new"
	p.Turns[0].Text = "不同的工作"
	call[accepted](t, e, "event.submit", p)
	waitNamingSignal(t, canceled)
	eventually(t, func() bool { return revisionRecord(t, e, "new").Status == "kept" })
	stale := revisionRecord(t, e, "old")
	if stale.Status != "stale" || len(stale.Attempts) != 1 || stale.Attempts[0].Status != "interrupted" || calls.Load() != 2 {
		t.Fatalf("supersession: %+v, %d calls", stale, calls.Load())
	}
}

func TestNamingThrottleCountsOnlyEligibleFollowUpsAndRespectsBypasses(t *testing.T) {
	dir := t.TempDir()
	e := engineAt(t, dir, nil)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	e.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	var calls atomic.Int32
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(usageReply))
	})
	p := event("first", "")
	p.Project = "C:/fixture/project"
	call[accepted](t, e, "event.submit", p)
	eventually(t, func() bool { return revisionRecord(t, e, "first").Status == "candidate" })
	c := call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, "", p.Revision}).Candidate
	if c == nil || !call[accepted](t, e, "title.ack", ackRequest{p.Agent, p.SessionID, c.ID, c.Title}).Accepted {
		t.Fatal("unable to seed managed title")
	}
	p.CurrentTitle = goodTitle
	submit := func(revision, text string, count int32, skip int) {
		t.Helper()
		p.Revision = revision
		p.Turns = append(p.Turns, turn{"user", text}, turn{"assistant", "ok"})
		call[accepted](t, e, "event.submit", p)
		eventually(t, func() bool { return revisionRecord(t, e, revision).Status == "kept" })
		var persisted int
		if err := e.db.QueryRow("SELECT throttle_skips FROM events WHERE revision=?", revision).Scan(&persisted); err != nil ||
			persisted != skip || calls.Load() != count {
			t.Fatalf("%s: %d model calls, %d skipped, %v", revision, calls.Load(), persisted, err)
		}
	}
	p.ConfirmationOnly = true
	submit("confirmation", "好的", 1, 0)
	p.ConfirmationOnly = false
	for n := 1; n <= 3; n++ {
		count, skips := int32(1), 1
		if n == 3 {
			count, skips = 2, 0
		}
		submit(fmt.Sprintf("append-%d", n), "follow up", count, skips)
		if n == 1 {
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			e = engineAt(t, dir, nil)
			e.now = func() time.Time { return time.UnixMilli(clock.Load()) }
		}
	}
	clock.Add(recheckCooldown.Milliseconds())
	submit("cooldown", "follow up after cooldown", 3, 0)
	p.Project = "C:/fixture/new-project"
	submit("project", "follow up in another project", 4, 0)
	p.Turns[0].Text = "改写此前目标"
	submit("edited", "follow up after an edited earlier turn", 5, 0)
}

func TestNamingThrottleMigratesV7History(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7,
		`INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id,orca_managed) VALUES('pi','history','r','Title',0,'chat','Title');
		 INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,sync_status,created,updated,finalized)
		 VALUES('prior','pi','history','r','','Title','applied','chat','auto','synced',1,2,2);
		 INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started,input_tokens)
		 VALUES('prior','pi','api','fixture','openai_compatible','default','completed',1,13);`,
	} {
		if _, err := db.Exec(script); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		e := engineAt(t, dir, nil)
		r := latest(t, e)
		var skips int
		if err := e.db.QueryRow("SELECT throttle_skips FROM events WHERE id='prior'").Scan(&skips); err != nil || skips != 0 {
			t.Fatalf("throttle migration: %d, %v", skips, err)
		}
		if r.ID != "prior" || r.Status != "applied" || r.SyncStatus != "synced" || len(r.Attempts) != 1 ||
			r.Attempts[0].InputTokens == nil || *r.Attempts[0].InputTokens != 13 {
			t.Fatalf("historical record changed: %+v", r)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func waitNamingSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("naming worker did not reach the expected step")
	}
}

func revisionRecord(t *testing.T, e *Engine, revision string) record {
	t.Helper()
	for _, item := range call[recordList](t, e, "records.list", listRequest{}).Items {
		if item.Revision == revision {
			return item
		}
	}
	t.Fatalf("missing revision %s", revision)
	return record{}
}
