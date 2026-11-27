package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestBoundProtectionReadsOnlyVerifiedChatAccount(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) { testBoundProtectionAccount(t, agent) })
	}
}

func testBoundProtectionAccount(t *testing.T, agent string) {
	variable, field := "CODEX_HOME", "threadId"
	if agent == "claude" {
		variable, field = "CLAUDE_CONFIG_DIR", "sessionId"
	}

	for _, tc := range []struct {
		name                                                                                                     string
		legacyHandle, defaultAccount, missingAccount, mismatchedThread, wrongMapping, accountChanged, readFailed bool
	}{
		{name: "separate account"}, {name: "verified default account", defaultAccount: true}, {name: "older event without a handle", legacyHandle: true},
		{name: "missing account", missingAccount: true}, {name: "conflicting native thread", mismatchedThread: true},
		{name: "conflicting Orca binding", wrongMapping: true}, {name: "account changed during read", accountChanged: true}, {name: "read failed", readFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const latest = "latest title in the verified account"
			home := t.TempDir()
			t.Setenv("ORCA_USER_DATA_PATH", home)
			defaultHome, account := t.TempDir(), t.TempDir()
			t.Setenv(variable, defaultHome)
			if tc.defaultAccount {
				account = defaultHome
			}
			domain := makeACPDatabase(t, home)
			thread, handle, lookup := nativeFixture, nativeFixture, acpChat
			if tc.legacyHandle {
				handle = ""
			}
			if tc.mismatchedThread {
				thread = acpNative
			}
			if tc.wrongMapping {
				lookup = manualChat
			}
			writeAccount := func(dir string) {
				t.Helper()
				record := map[string]any{"sessionId": acpChat, "schemaVersion": 2, "provider": agent,
					"location":            map[string]string{"executionHostId": "local", "workspaceKind": "git-worktree"},
					"providerHandleChain": []any{map[string]any{"handle": map[string]string{"provider": agent, field: thread}}}}
				if dir != "" {
					record["accountHome"] = map[string]string{"variable": variable, "path": dir}
				}
				wire, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := domain.Exec(`INSERT INTO agent_session_records VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET record_json=excluded.record_json`, acpChat, string(wire)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.missingAccount {
				writeAccount("")
			} else {
				writeAccount(account)
			}
			reads := 0
			i := Integration{Enabled: alwaysEnabled, origin: func(context.Context, string, string, string) string { return titleManual }}
			i.ReadNative = func(context.Context, string, string, string) (string, error) {
				t.Fatal("bound chat read unverified default account")
				return "", nil
			}
			i.ReadNativeAt = func(_ context.Context, gotAgent, id, dir string) (string, error) {
				reads++
				if gotAgent != agent || id != nativeFixture || !samePath(dir, account) {
					t.Fatalf("read wrong native ID or account: %q %q", id, dir)
				}
				if tc.accountChanged {
					writeAccount(defaultHome)
				}
				if tc.readFailed {
					return "", ErrNativePending
				}
				return latest, nil
			}
			i.run = func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "agent" && args[1] == "show" {
					switch args[2] {
					case nativeFixture:
						return providerMapping(lookup)
					case idPrefix + acpChat:
						title := latest
						return replyPlace(place{ID: acpChat, Agent: agent, Kind: "chat", HostID: "local", Title: &title})
					}
				}
				return nil, fmt.Errorf("unexpected Orca command: %v", args)
			}
			native, orca, err := i.ReadProtectionTitles(t.Context(), agent, nativeFixture, handle, acpChat)
			var want error
			wantReads := 1
			switch {
			case tc.wrongMapping:
				want, wantReads = errProtectionBinding, 0
			case tc.missingAccount || tc.mismatchedThread:
				want, wantReads = errProtectionNative, 0
			case tc.accountChanged:
				want = errProtectionBinding
			case tc.readFailed:
				want = errProtectionNative
			}
			if !errors.Is(err, want) || reads != wantReads {
				t.Fatalf("got %q %v %v, reads=%d; want %v, reads=%d", native, orca, err, reads, want, wantReads)
			}
			if want != nil && (native != "" || orca != nil) {
				t.Fatal("failed read returned titles")
			}
			if want == nil && (native != latest || orca == nil || *orca != latest) {
				t.Fatal("verified titles missing")
			}
		})
	}
}

func TestStandaloneCodexProtectionUsesDefaultNativeReader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	i := Integration{ReadNative: func(_ context.Context, agent, id, handle string) (string, error) {
		if agent != "codex" || id != nativeFixture || handle != "" {
			t.Fatalf("unexpected standalone identity: %s %s %s", agent, id, handle)
		}
		return filepath.Base(home), nil
	}}
	native, orca, err := i.ReadProtectionTitles(t.Context(), "codex", nativeFixture, "", "")
	if native != filepath.Base(home) || orca != nil || err != nil {
		t.Fatalf("standalone read: %q %v %v", native, orca, err)
	}
}
