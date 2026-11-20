package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
)

func piTitleFile(t *testing.T, title string) (string, func(string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "fixture_"+nativeFixture+".jsonl")
	write := func(title string) {
		t.Helper()
		header := `{"type":"session","version":3,"id":"` + nativeFixture + `"}` + "\n"
		frame, _ := json.Marshal(map[string]string{"type": "session_info", "name": title})
		if err := os.WriteFile(file, []byte(header+string(frame)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(title)
	return file, write
}

func TestVerifiedSyncProtectsManualPiRenameBeforeNextHook(t *testing.T) {
	for _, run := range []struct {
		name, native, orca string
		wantErr            error
		writes             int
	}{
		{"retry after unreported manual rename", "手动标题 B", "Orca 原名 X", ErrNativeStale, 0},
		{"adopt after unreported manual rename", "手动标题 B", "Orca 手动名 X", ErrNativeStale, 0},
		{"retry after native title read failure", "", "Orca 原名 X", ErrNativePending, 0},
		{"matching native title", "规范标题", "Orca 原名 X", nil, 1},
		{"target already shown in Orca but stale native title", "手动标题 B", "规范标题", ErrNativeStale, 0},
	} {
		t.Run(run.name, func(t *testing.T) {
			handle, write := piTitleFile(t, "规范标题")
			if run.native != "" {
				write(run.native)
			}
			fixture := &cliFixture{t: t, agent: "pi", title: run.orca, write: true}
			runCLI := func(ctx context.Context, args ...string) ([]byte, error) {
				if args[0] == "agent" && args[1] == "show" && args[2] == handle {
					return []byte(`{"ok":false,"error":{"code":"agent_provider_id","message":"Address it as orca_session_id:codex_chat."}}`), errors.New("provider diagnostic")
				}
				if args[0] == "chat" && args[1] == "rename" {
					write(args[5]) // Orca also writes the Pi session name.
				}
				return fixture.run(ctx, args...)
			}
			reader := func(ctx context.Context, _, sessionID, handle string) (string, error) {
				if run.native == "" {
					return "", os.ErrPermission
				}
				return pluginmgr.ReadPiTitle(ctx, sessionID, handle)
			}
			err := syncWithChecks(context.Background(), runCLI, "codex_chat", run.orca, "规范标题", "pi", nativeFixture, handle, syncChecks{read: reader, validate: (&pluginmgr.Manager{}).ValidateNativeHandle, origin: databaseOrigin})
			if !errors.Is(err, run.wantErr) || fixture.writes != run.writes {
				t.Fatalf("sync returned %v with %d writes", err, fixture.writes)
			}
			if run.writes == 0 && fixture.title != run.orca {
				t.Fatal("Orca title changed without an approved write")
			}
			if got, err := pluginmgr.ReadPiTitle(context.Background(), nativeFixture, handle); err != nil ||
				run.native != "" && got != run.native {
				t.Fatalf("native title after sync: %q, %v", got, err)
			}
		})
	}
}
