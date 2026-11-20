package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

type cliFixture struct {
	t          *testing.T
	agent      string
	title      string
	showTitle  *string
	duplicates bool
	remote     bool
	write      bool
	failAck    bool
	calls      int
	writes     int
}

func (f *cliFixture) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls++
	if !reflect.DeepEqual(args[len(args)-3:], []string{"--environment", "local", "--json"}) {
		f.t.Fatalf("wrong CLI scope: %v", args)
	}
	agent := f.agent
	if agent == "" {
		agent = "codex"
	}
	at := place{ID: "codex_chat", Agent: agent, Kind: "chat", HostID: "local", Title: &f.title}
	if f.remote {
		at.HostID = "remote"
	}
	var result any
	switch args[0] + " " + args[1] {
	case "agent list":
		agents := []place{at}
		if f.duplicates {
			agents = append(agents, at)
		}
		result = map[string]any{"truncated": false, "agents": agents}
	case "agent show":
		if args[2] != "orca_session_id:codex_chat" {
			f.t.Fatalf("wrong show identity: %s", args[2])
		}
		if f.showTitle != nil {
			at.Title = f.showTitle
		}
		result = map[string]any{"orcaSessionId": at.ID, "kind": "chat", "places": []place{at}}
	case "chat rename":
		if !reflect.DeepEqual(args[2:6], []string{"--session", "codex_chat", "--name", "规范标题"}) {
			f.t.Fatalf("wrong rename arguments: %v", args)
		}
		f.writes++
		if f.write {
			f.title = args[5]
		}
		if f.failAck {
			return nil, errors.New("transport interrupted")
		}
	default:
		f.t.Fatalf("unexpected command: %v", args)
	}
	return json.Marshal(map[string]any{"ok": true, "result": result})
}

func TestSyncUsesAuthorizedBaseline(t *testing.T) {
	different := "changed during lookup"
	for _, c := range []struct {
		name, id, baseline, current string
		override                    *string
		duplicate, remote, write    bool
		wantErr                     error
		writes                      int
	}{
		{name: "empty initial title", id: "codex_chat", write: true, writes: 1},
		{name: "authorized prior title", id: "orca_session_id:codex_chat", baseline: "不同的旧标题", current: "不同的旧标题", write: true, writes: 1},
		{name: "manual title", id: "codex_chat", baseline: "旧标题", current: "手工设置", wantErr: ErrSkipped},
		{name: "unknown nonempty title", id: "codex_chat", current: "来源未知", wantErr: ErrSkipped},
		{name: "conflicting lookups", id: "codex_chat", current: "旧标题", override: &different, wantErr: ErrSkipped},
		{name: "ambiguous handle", id: "codex_chat", duplicate: true, wantErr: ErrSkipped},
		{name: "remote handle", id: "codex_chat", remote: true, wantErr: ErrSkipped},
		{name: "missing handle", wantErr: ErrSkipped},
		{name: "no write on retry", id: "codex_chat", baseline: "旧标题", current: "规范标题"},
		{name: "unconfirmed write", id: "codex_chat", baseline: "old", current: "old", wantErr: ErrUnconfirmed, writes: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &cliFixture{t: t, title: c.current, showTitle: c.override, duplicates: c.duplicate, remote: c.remote, write: c.write}
			err := syncTitle(context.Background(), f.run, c.id, c.baseline, "规范标题", allowWrite)
			if !errors.Is(err, c.wantErr) || f.writes != c.writes {
				t.Fatalf("sync returned %v and %d writes; want %v and %d", err, f.writes, c.wantErr, c.writes)
			}
			if c.id == "" && f.calls != 0 {
				t.Fatal("made CLI calls for missing identity")
			}
		})
	}
}

func TestSyncRecoversLostWriteResponseByReadingTitle(t *testing.T) {
	f := &cliFixture{t: t, title: "old", write: true, failAck: true}
	if err := syncTitle(context.Background(), f.run, "codex_chat", "old", "规范标题", allowWrite); err != nil || f.writes != 1 {
		t.Fatalf("status %v, writes %d", err, f.writes)
	}
}

func allowWrite(context.Context, place) error { return nil }

func TestInstalledOrcaReadOnly(t *testing.T) {
	id, native := os.Getenv("ATM_TEST_ORCA_ID"), os.Getenv("ATM_TEST_ORCA_NATIVE_ID")
	if id == "" || native == "" {
		t.Skip("requires explicit read-only Orca identifiers")
	}
	if _, bound := discover(context.Background(), localCLI, nil, "codex", native, native, id, "", []time.Duration{0}); !bound {
		t.Fatal("installed Orca CLI did not validate the supplied binding")
	}
	if _, err := observe(context.Background(), localCLI, id); err != nil {
		t.Fatalf("installed Orca CLI read-only title lookup: %v", err)
	}
}
