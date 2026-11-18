package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
				return ReadPiTitle(ctx, sessionID, handle)
			}
			err := syncVerified(context.Background(), runCLI, "codex_chat", run.orca, "规范标题", "pi", nativeFixture, handle, reader)
			if !errors.Is(err, run.wantErr) || fixture.writes != run.writes {
				t.Fatalf("sync returned %v with %d writes", err, fixture.writes)
			}
			if run.writes == 0 && fixture.title != run.orca {
				t.Fatal("Orca title changed without an approved write")
			}
			if got, err := ReadPiTitle(context.Background(), nativeFixture, handle); err != nil ||
				run.native != "" && got != run.native {
				t.Fatalf("native title after sync: %q, %v", got, err)
			}
		})
	}
}

func TestPiTitleReaderRequiresCompleteKnownSession(t *testing.T) {
	handle, _ := piTitleFile(t, "A")
	file, err := os.OpenFile(handle, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("{\"type\":\"session_info\",\"name\":\"\"}\n")
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	if got, err := ReadPiTitle(context.Background(), nativeFixture, handle); err != nil || got != "" {
		t.Fatalf("cleared name resolved as %q, %v", got, err)
	}
	wire, err := os.ReadFile(handle)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{strings.Replace(string(wire), "\"version\":3", "\"version\":4", 1), string(wire) + "{"} {
		if err := os.WriteFile(handle, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadPiTitle(context.Background(), nativeFixture, handle); !errors.Is(err, ErrNativePending) {
			t.Fatalf("unverified name %q with error %v", got, err)
		}
	}
}
