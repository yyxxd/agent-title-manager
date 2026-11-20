package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeFixture = "12345678-1234-1234-1234-123456789abc"

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
func TestPiBindingRequiresSessionFileOfSameNativeID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.Mkdir(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	name := "2026-11-18T01-02-03_" + nativeFixture + ".jsonl"
	path := filepath.Join(sessions, name)
	put := func(path, head string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(head+"\n{malformed second line"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(path, `{"type":"session","id":"`+nativeFixture+`"}`)
	if !(&Manager{}).ValidateNativeHandle(context.Background(), "pi", nativeFixture, path) {
		t.Fatal("valid header rejected")
	}
	put(path, `{"type":"session","id":"wrong"}`)
	if (&Manager{}).ValidateNativeHandle(context.Background(), "pi", nativeFixture, path) {
		t.Fatal("incorrect session header accepted")
	}
	outside := filepath.Join(home, name)
	put(outside, `{"type":"session","id":"`+nativeFixture+`"}`)
	if (&Manager{}).ValidateNativeHandle(context.Background(), "pi", nativeFixture, outside) {
		t.Fatal("outside file accepted")
	}
}
