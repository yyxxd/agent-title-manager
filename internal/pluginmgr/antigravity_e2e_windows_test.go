package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/ipc"
	"golang.org/x/sys/windows"
)

func TestAntigravityIsolatedCmdHookRoundTripWithRealBridge(t *testing.T) {
	if _, err := exec.LookPath("node.exe"); err != nil {
		t.Skip("Node.js is unavailable")
	}
	root := t.TempDir()
	profile := filepath.Join(root, "ATM Data")
	host := filepath.Join(root, "Gemini Config")
	data := filepath.Join(root, "Native Sessions")
	bundle := filepath.Join(root, "Bundled Runtime", "plugins")
	t.Setenv("ATM_DATA_DIR", profile)
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", host)
	t.Setenv("ANTIGRAVITY_DATA_DIR", data)
	t.Setenv("ATM_ANTIGRAVITY_WORKER", "")
	t.Setenv("ORCA_CLI_COMMAND", filepath.Join(root, "orca-offline.exe"))
	t.Setenv("ORCA_USER_DATA_PATH", filepath.Join(root, "Orca Data"))
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	source, err := filepath.Abs("../../plugins/antigravity")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(bundle, "antigravity")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyLocal(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(filepath.Dir(bundle), "atm-bridge.exe")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bridge, "./cmd/atm-bridge")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test bridge: %v\n%s", err, out)
	}

	_, scope, err := ipc.Profile()
	if err != nil || scope == "" {
		t.Fatal("isolated pipe scope required")
	}
	listener, err := ipc.Listen(scope)
	if err != nil {
		t.Fatal(err)
	}
	const (
		id      = "b68b5391-bfed-45ae-948e-259bb4abf012"
		title   = "修复标题｜隔离验收"
		private = "synthetic-body-must-not-enter-diagnostics"
	)
	path := filepath.Join(data, "brain", id, ".system_generated", "logs", "transcript.jsonl")
	writeFixture(t, path, `{"type":"USER_INPUT","source":"USER_EXPLICIT","step_index":1,"content":"`+private+`"}`+"\n"+
		`{"type":"PLANNER_RESPONSE","source":"MODEL","step_index":2,"content":"synthetic response","status":"DONE"}`+"\n")
	serverDone := make(chan error, 1)
	acknowledged := make(chan struct{}, 1)
	var mu sync.Mutex
	var revision string
	go func() {
		serverDone <- ipc.Serve(ctx, listener, func(_ context.Context, method string, body json.RawMessage) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			var request map[string]any
			if json.Unmarshal(body, &request) != nil || request["agent"] != "antigravity" || request["sessionId"] != id {
				return nil, errors.New("unexpected fixture request")
			}
			switch method {
			case "event.submit":
				revision, _ = request["revision"].(string)
				if revision == "" || !strings.Contains(string(body), private) {
					return nil, errors.New("snapshot missing")
				}
				return map[string]bool{"accepted": true}, nil
			case "title.claim":
				if revision == "" || request["revision"] != revision || request["currentTitle"] != "" {
					return nil, errors.New("incorrect candidate claim")
				}
				return map[string]any{"candidate": map[string]string{"id": "fixture", "title": title, "baseTitle": "", "revision": revision}}, nil
			case "title.ack":
				if request["id"] != "fixture" || request["observedTitle"] != title {
					return nil, errors.New("incorrect title readback")
				}
				acknowledged <- struct{}{}
				return map[string]any{"accepted": true, "syncStatus": "unbound"}, nil
			}
			return nil, errors.New("unknown test method")
		})
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-serverDone; err != nil {
			t.Error(err)
		}
	})

	m := New(profile, bundle)
	mustInvoke(t, m, "enable", request{ID: "antigravity"})
	state, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	dir := m.packageDir(state.Items["antigravity"].release)
	var settings map[string]map[string][]struct{ Command string }
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join(host, "hooks.json"))), &settings); err != nil {
		t.Fatal(err)
	}
	command := settings["agent-title-manager"]["Stop"][0].Command
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	cmdPath := filepath.Join(systemDir, "cmd.exe")
	cmd = exec.CommandContext(ctx, cmdPath)
	hideProcess(cmd)
	cmd.SysProcAttr.CmdLine = `"` + cmdPath + `" /d /c "` + command + `"`
	input, _ := json.Marshal(map[string]string{"conversation_id": id, "transcript_path": path, "hookEventName": "Stop", "unused": private})
	cmd.Stdin = strings.NewReader(string(input))
	if out, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != `{"decision":""}` {
		t.Fatalf("hook response %q, %v", out, err)
	}
	select {
	case <-acknowledged:
	case <-ctx.Done():
		wire, _ := os.ReadFile(filepath.Join(dir, "antigravity-diagnostics.log"))
		t.Fatalf("no readback acknowledgement: %s", wire)
	}
	native := readFixture(t, filepath.Join(data, "annotations", id+".pbtxt"))
	if !strings.Contains(native, title) {
		t.Fatalf("native annotation missing confirmed title: %q", native)
	}
	var diagnostics string
	for ctx.Err() == nil {
		diagnostics = readFixture(t, filepath.Join(dir, "antigravity-diagnostics.log"))
		if strings.Contains(diagnostics, "worker.completed") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, stage := range []string{"hook.received", "submit.accepted", "claim.ready", "readback.confirmed", "worker.completed"} {
		if !strings.Contains(diagnostics, stage) {
			t.Fatalf("missing stage %s in %s", stage, diagnostics)
		}
	}
	if strings.Contains(diagnostics, private) || strings.Contains(diagnostics, title) || strings.Contains(diagnostics, id) {
		t.Fatal("diagnostics leaked conversation data")
	}
	t.Log("cmd.exe → installed Hook → isolated Windows named pipe → submit/claim → native annotation → acknowledged readback")
}
