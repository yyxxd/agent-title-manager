package orcasync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestACPObserverAcceptsWindowsShortNames(t *testing.T) {
	short, long := shortTestDirectory(t)
	db := makeACPDatabase(t, long)
	if _, err := db.Exec(`INSERT INTO agent_session_records VALUES ('auto-chat', '{"schemaVersion":2,"sessionId":"auto-chat","provider":"pi"}')`); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(short, "agent-session-domain.db")
	if !safeDatabase(file) {
		t.Fatalf("ordinary database reached through 8.3 alias was rejected: %s", file)
	}
	if got := titleOrigin(t.Context(), file, "pi", "auto-chat", "Pi Chat"); got != titleAuto {
		t.Fatalf("title origin through 8.3 alias: %s", got)
	}
	longObserver := filepath.Join(long, "observer")
	if err := os.Mkdir(longObserver, 0700); err != nil {
		t.Fatal(err)
	}
	testACPObserver(t, filepath.Join(short, "observer"), filepath.Join(longObserver, "antigravity-chat"))
}

func shortTestDirectory(t *testing.T) (short, long string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Orca directory with a long name")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("GetShortPathName: length %d, error %v", n, err)
	}
	short = windows.UTF16ToString(buf[:n])
	long, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(short, long) {
		t.Skip("8.3 aliases are disabled on the temporary volume")
	}
	return short, long
}

func TestOrcaDatabaseRejectsWindowsJunctionAncestor(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real-parent")
	home := filepath.Join(realParent, "orca")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	makeACPDatabase(t, home)
	junction := filepath.Join(root, "junction")
	cmd := exec.CommandContext(t.Context(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", junction, realParent)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, out)
	}
	if path := filepath.Join(junction, "orca", "agent-session-domain.db"); safeDatabase(path) {
		t.Fatalf("accepted database redirected through ancestor junction: %s", path)
	}
	if samePath(filepath.Join(junction, "orca"), home) {
		t.Fatal("junction alias was accepted as an account home spelling")
	}
}
