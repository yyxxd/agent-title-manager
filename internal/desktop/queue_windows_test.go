package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIngressFullPreservesSessionOrderAndDrains(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var revisions []string
	ingress := startIngress(parent, Backend{
		Enabled: func(context.Context, string) (bool, error) { return true, nil },
		ResolveTitleOrigin: func(context.Context, string, string, string, string) (string, string, bool) {
			close(started)
			<-release
			return "原名", "auto", true
		},
		Engine: func(_ context.Context, _ string, raw json.RawMessage) (any, error) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, err
			}
			if fieldString(fields, "originalGoal") != "测试" || string(fields["confirmationOnly"]) != "true" {
				t.Error("lost adapter fields")
			}
			revisions = append(revisions, fieldString(fields, "revision"))
			return nil, nil
		},
	})
	testQueueCapacity(t, ingress, started)
	cancel()
	close(release)
	ingress.stop()
	if len(revisions) != cap(ingress.queue)+1 {
		t.Fatalf("accepted events were lost: %v", revisions)
	}
	for n, got := range revisions {
		if got != fmt.Sprint(n) {
			t.Fatalf("events ran out of order: %v", revisions)
		}
	}
}

func testQueueCapacity(t *testing.T, ingress *eventIngress, started <-chan struct{}) {
	t.Helper()
	first := queueFixture(0)
	first.fields["orcaNativeHandle"] = json.RawMessage(`"native"`)
	first.fields["orcaSessionId"] = json.RawMessage(`"orca"`)
	expectQueued(t, ingress, first, false)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first binding was not started")
	}
	expectQueued(t, ingress, first, true)
	for n := 1; n <= cap(ingress.queue); n++ {
		expectQueued(t, ingress, queueFixture(n), false)
	}
	queued, _, err := ingress.add(queueFixture(cap(ingress.queue) + 1))
	if queued || err == nil || !strings.HasPrefix(err.Error(), "ingress_busy:") {
		t.Fatalf("newer session event overtook the queue: %v %v", queued, err)
	}
	other := queueFixture(0)
	other.key.session = "unrelated-session"
	if queued, _, err := ingress.add(other); queued || err != nil {
		t.Fatalf("unrelated native event was delayed: %v %v", queued, err)
	}
}

func queueFixture(n int) pendingEvent {
	revision := fmt.Sprint(n)
	return pendingEvent{
		key: eventKey{agent: "codex", session: "session", revision: revision},
		fields: map[string]json.RawMessage{
			"revision":     json.RawMessage(fmt.Sprintf("%q", revision)),
			"originalGoal": json.RawMessage(`"测试"`), "confirmationOnly": json.RawMessage("true"),
		},
	}
}

func expectQueued(t *testing.T, ingress *eventIngress, event pendingEvent, duplicate bool) {
	t.Helper()
	queued, dup, err := ingress.add(event)
	if !queued || err != nil || dup != duplicate {
		t.Fatalf("unexpected enqueue result: %v %v %v", queued, dup, err)
	}
}
