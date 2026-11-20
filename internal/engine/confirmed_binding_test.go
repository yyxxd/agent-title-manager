package engine

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestConfirmedBindingAttachesToExistingNativeRenameOnce(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current revision"
		if stale {
			name = "newer revision"
		}
		t.Run(name, func(t *testing.T) {
			var writes atomic.Int64
			e := engineAt(t, t.TempDir(), func(ctx context.Context, id, expected, next string) error {
				target, ok := SyncTargetFromContext(ctx)
				if !ok || id != "chat" || expected != "" || next != goodTitle || target.NativeHandle != "native" {
					t.Errorf("unexpected live sync: %+v", target)
				}
				writes.Add(1)
				return nil
			})
			httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(usageReply)) })
			p := event("first", "")
			call[accepted](t, e, "event.submit", p)
			eventually(t, func() bool { return latest(t, e).Status == "candidate" })
			c := call[claimResponse](t, e, "title.claim", claimRequest{p.Agent, p.SessionID, "", p.Revision}).Candidate
			if c == nil {
				t.Fatal("candidate missing")
			}
			_, _, unbound := UnboundAckRevisions(call[accepted](t, e, "title.ack", ackRequest{p.Agent, p.SessionID, c.ID, c.Title}))
			if !unbound {
				t.Fatal("expected applied, unbound native rename")
			}
			if stale {
				// 同名的新一轮仍不能继承旧回合的异步绑定。
				p.Revision, p.CurrentTitle, p.Turns = "newer", c.Title, []turn{}
				call[accepted](t, e, "event.submit", p)
			}
			binding := ConfirmedBinding{ID: c.ID, Agent: p.Agent, SessionID: p.SessionID, Revision: "first",
				NativeTitle: c.Title, OrcaID: "chat", Origin: "auto", NativeHandle: "native"}
			for range 2 {
				if err := e.AttachBinding(t.Context(), binding); err != nil {
					t.Fatal(err)
				}
			}
			if stale {
				var status string
				if err := e.db.QueryRow("SELECT sync_status FROM events WHERE id=?", c.ID).Scan(&status); err != nil || status != "unbound" || writes.Load() != 0 {
					t.Fatalf("stale event attached: %s, %d writes, %v", status, writes.Load(), err)
				}
				return
			}
			eventually(t, func() bool { return latest(t, e).SyncStatus == "synced" })
			items := call[recordList](t, e, "records.list", listRequest{}).Items
			if len(items) != 1 || items[0].ID != c.ID || writes.Load() != 1 {
				t.Fatalf("late binding duplicated the rename: %v, %d writes", items, writes.Load())
			}
		})
	}
}
