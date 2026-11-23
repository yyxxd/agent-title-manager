package orcasync

import (
	"context"
	"fmt"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func TestRecordNamesFollowPublicSessionIdentities(t *testing.T) {
	i := Integration{Enabled: alwaysEnabled, run: func(_ context.Context, args ...string) ([]byte, error) {
		var body string
		switch args[0] + " " + args[1] {
		case "agent list":
			body = `{"agents":[{"orcaSessionId":"orca_session_id:project-a","agent":"codex","hostId":"local","repo":"Alpha"},
				{"orcaSessionId":"project-b","agent":"codex","hostId":"local","repo":"Beta"},
				{"orcaSessionId":"bot-unavailable","agent":"codex","hostId":"local","repo":"Do not label as project"},
				{"orcaSessionId":"remote","agent":"codex","hostId":"remote","repo":"Unknown"}]}`
		case "bot list":
			body = `{"bots":[{"id":"bot1","name":"First bot"},{"id":"bot2","name":"Second bot"}]}`
		case "bot chats":
			switch args[3] {
			case "bot1":
				body = `{"chats":[{"id":"topic-a"},{"id":"orca_session_id:topic-b"},{"id":"ambiguous"}]}`
			case "bot2":
				body = `{"chats":[{"id":"topic-c"},{"id":"ambiguous"}]}`
			}
		}
		if body == "" {
			return nil, fmt.Errorf("unexpected command %v", args)
		}
		return []byte(`{"ok":true,"result":` + body + "}"), nil
	}}
	var keys []engine.RecordIdentity
	for _, id := range []string{"project-a", "project-b", "topic-a", "topic-b", "topic-c", "remote", "ambiguous", "bot-unavailable"} {
		key := engine.RecordIdentity{Agent: "codex", OrcaID: id}
		if id == "bot-unavailable" {
			key.Kind = "bot"
		}
		keys = append(keys, key)
	}
	got := i.RecordNames(t.Context(), keys)
	if len(got) != 5 || got["project-a"].Name != "Alpha" || got["project-b"].Name != "Beta" ||
		got["topic-a"].Name != "First bot" || got["topic-b"].Name != "First bot" || got["topic-c"].Name != "Second bot" {
		t.Fatalf("incorrect session name attribution: %+v", got)
	}
}
