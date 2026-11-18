package pluginmgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCodexHookCommandWindowsShells(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	dir := filepath.Join(t.TempDir(), "Hook's package & runtime")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "stop hook.mjs")
	writeFixture(t, entry, `process.stdout.write(process.argv[2] + ":"); process.stdin.pipe(process.stdout)`)
	prefix, err := hookEntry("codex", node, entry)
	if err != nil {
		t.Fatal(err)
	}
	command := prefix + " stop"
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	const input = `{"hook_event_name":"Stop","turn_id":"isolated-turn"}`
	for _, shell := range []string{"cmd.exe", "WindowsPowerShell/v1.0/powershell.exe"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			program := filepath.Join(systemDir, filepath.FromSlash(shell))
			cmd := exec.Command(program, "-NoProfile", "-Command", command)
			hideProcess(cmd)
			if shell == "cmd.exe" {
				cmd = exec.Command(program)
				hideProcess(cmd)
				cmd.SysProcAttr.CmdLine = `"` + program + `" /D /C "` + command + `"`
			}
			cmd.Stdin = strings.NewReader(input)
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "stop:"+input {
				t.Fatalf("hook did not receive its mode and stdin: %q (%v)", out, err)
			}
		})
	}
}
