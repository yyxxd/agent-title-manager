package pluginmgr

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

// RunAntigravityObserver 随桌面服务启停，并跟随已启用插件的安装版本。
func (m *Manager) RunAntigravityObserver(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var path string
	var cancel context.CancelFunc
	var done <-chan error
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel, done, path = nil, nil, ""
		}
	}
	defer stop()
	for {
		next := m.antigravityObserverPath(ctx)
		if next != path {
			stop()
			if next != "" && ctx.Err() == nil {
				var err error
				cancel, done, err = m.startAntigravityObserver(ctx, next)
				if err != nil {
					appendAntigravityDiagnostic(filepath.Dir(next), "observer.launch.failed")
				} else {
					path = next
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-done:
			if ctx.Err() == nil {
				appendAntigravityDiagnostic(filepath.Dir(path), "observer.exited")
			}
			cancel()
			cancel, done, path = nil, nil, ""
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func (m *Manager) antigravityObserverPath(ctx context.Context) string {
	enabled, err := m.Enabled(ctx, "antigravity")
	if err != nil || !enabled {
		return ""
	}
	state, err := m.load()
	if err != nil || state.Pending != nil && state.Pending.ID == "antigravity" {
		return ""
	}
	r := state.Items["antigravity"]
	if !r.Enabled || r.Dir == "" || r.Manifest.ID != "antigravity" {
		return ""
	}
	entry := filepath.Join(m.packageDir(r.release), "observe.mjs")
	if info, err := os.Lstat(entry); err == nil && info.Mode().IsRegular() && !isReparse(info) {
		return entry
	}
	return ""
}

func (m *Manager) startAntigravityObserver(parent context.Context, entry string) (context.CancelFunc, <-chan error, error) {
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, node, entry)
	hideProcess(cmd)
	cmd.Env = withoutNodeInjection(cmd.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cancel, done, nil
}

func appendAntigravityDiagnostic(dir, stage string) {
	file := filepath.Join(dir, "antigravity-diagnostics.log")
	if info, err := os.Lstat(file); err == nil && (!info.Mode().IsRegular() || isReparse(info) || info.Size() >= 128*1024) {
		return
	}
	logFile, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err == nil {
		_, err = fmt.Fprintln(logFile, time.Now().UTC().Format(time.RFC3339Nano), stage)
		_ = logFile.Close()
	}
	if err != nil {
		log.Print(stage)
	}
}

func (m *Manager) recordAntigravitySync(syncErr error) {
	state, err := m.load()
	r := state.Items["antigravity"]
	if err != nil || r.Dir == "" {
		return
	}
	stage := "orca.sync.failed"
	switch {
	case syncErr == nil:
		stage = "orca.sync.confirmed"
	case errors.Is(syncErr, orcasync.ErrNativePending):
		stage = "orca.sync.native_pending"
	case errors.Is(syncErr, orcasync.ErrNativeStale):
		stage = "orca.sync.native_stale"
	case errors.Is(syncErr, orcasync.ErrSkipped):
		stage = "orca.sync.skipped"
	}
	appendAntigravityDiagnostic(m.packageDir(r.release), stage)
}
