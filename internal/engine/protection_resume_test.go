package engine

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestProtectedPiAndAntigravityResumeNamingAfterRelease(t *testing.T) {
	for _, tc := range []struct{ name, agent, orcaID, wantSync string }{
		{"standalone Pi", "pi", "", "unbound"},
		{"standalone Antigravity", "antigravity", "", "unbound"},
		{"Antigravity Orca Chat", "antigravity", "orca-chat", "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int64
			var syncCalls atomic.Int64
			e := engineAt(t, t.TempDir(), func(context.Context, string, string, string) error { syncCalls.Add(1); return nil })
			httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) { attempts.Add(1); _, _ = w.Write([]byte(usageReply)) })
			const original = "Protected native title"
			p := submission{Agent: tc.agent, SessionID: "standalone-or-bound", Revision: "before", CurrentTitle: original, TitleOrigin: "unknown", Turns: []turn{}}
			if tc.orcaID != "" {
				name := "Antigravity Chat"
				p.OrcaID, p.OrcaTitle, p.OrcaOrigin, p.OrcaNativeHandle = tc.orcaID, &name, "auto", p.SessionID
			}
			if !call[accepted](t, e, "event.submit", p).Accepted || latest(t, e).Reason != decisionProtected {
				t.Fatal("initial title was not protected")
			}
			target := call[ProtectionTarget](t, e, "records.protectionTarget", idRequest{latest(t, e).ID})
			if target.OrcaID != tc.orcaID || target.NativeHandle != p.OrcaNativeHandle || target.Baseline != original {
				t.Fatalf("unexpected protection target: %+v", target)
			}
			if !call[accepted](t, e, "records.releaseProtection", protectionRelease{ProtectionTarget: target, NativeTitle: original, OrcaTitle: p.OrcaTitle}).Accepted {
				t.Fatal("verified unlock failed")
			}
			state, err := loadSession(t.Context(), e.db, p.Agent, p.SessionID)
			if err != nil || state.manual || state.title != original || attempts.Load() != 0 || syncCalls.Load() != 0 {
				t.Fatalf("unlock changed state beyond protection: %+v, %v", state, err)
			}
			p.Revision = "next-completed-turn"
			p.Turns = []turn{{"user", "Please repair the update check"}, {"assistant", "I found the mismatched path"}}
			if !call[accepted](t, e, "event.submit", p).Accepted {
				t.Fatal("eligible turn rejected")
			}
			eventually(t, func() bool { return latest(t, e).Status == "candidate" })
			claim := call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, original, p.Revision}).Candidate
			if claim == nil || claim.Title != goodTitle {
				t.Fatalf("unexpected candidate: %+v", claim)
			}
			ack := call[accepted](t, e, "title.ack", ackRequest{Agent: p.Agent, SessionID: p.SessionID, ID: claim.ID, ObservedTitle: claim.Title})
			if !ack.Accepted || ack.SyncStatus != tc.wantSync || attempts.Load() != 1 || syncCalls.Load() != 0 {
				t.Fatalf("post-unlock ack: %+v; attempts=%d syncs=%d", ack, attempts.Load(), syncCalls.Load())
			}
			t.Logf("lock=%s; unlock read and kept %q; next=%q; sync=%s", decisionProtected, original, claim.Title, ack.SyncStatus)
		})
	}
}
