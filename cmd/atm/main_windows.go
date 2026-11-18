package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yyxxd/agent-title-manager/frontend"
	"github.com/yyxxd/agent-title-manager/internal/desktop"
	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/ipc"
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--check-uninstall" {
		os.Exit(uninstallStatus())
	}
	if err := run(); err != nil {
		message, _ := windows.UTF16PtrFromString(fmt.Sprintf("桌面服务启动失败\n%v", err))
		title, _ := windows.UTF16PtrFromString("Agent Title Manager")
		windows.MessageBox(0, message, title, windows.MB_OK|windows.MB_ICONERROR)
		os.Exit(1)
	}
}

func run() error {
	dir, scope, err := ipc.Profile()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := filepath.Join(filepath.Dir(exe), "plugins")
	service := desktop.NewService(scope, func() (desktop.Backend, error) {
		plugins := pluginmgr.New(dir, bundle)
		core, err := engine.Open(dir, syncer(plugins), func(ctx context.Context, id string) bool {
			enabled, err := plugins.Enabled(ctx, id)
			return err == nil && enabled
		})
		if err != nil {
			return desktop.Backend{}, err
		}
		return desktop.Backend{
			Engine: core.Handle, Plugins: plugins.Handle, Enabled: plugins.Enabled,
			ResolveTitleOrigin: orcasync.ResolveTitleOrigin, Close: core.Close,
		}, nil
	})
	return desktop.Run(dir, frontend.Assets(), service)
}

func syncer(plugins *pluginmgr.Manager) func(context.Context, string, string, string) error {
	return func(ctx context.Context, id, expected, title string) error {
		target, ok := engine.SyncTargetFromContext(ctx)
		if !ok {
			return orcasync.ErrNativePending
		}
		if target.ExpectedTitle != title {
			return engine.ErrSyncStale
		}
		err := plugins.SyncVerified(ctx, id, expected, title, target.Agent, target.SessionID, target.NativeHandle)
		switch {
		case errors.Is(err, orcasync.ErrSkipped):
			return engine.ErrSyncSkipped
		case errors.Is(err, orcasync.ErrNativeStale):
			return engine.ErrSyncStale
		default:
			return err
		}
	}
}

func uninstallStatus() int {
	dir, scope, err := ipc.Profile()
	exe, exeErr := os.Executable()
	if err != nil || exeErr != nil {
		return 3
	}
	running, err := ipc.IsRunning(scope)
	if err != nil {
		return 3
	}
	if running {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	plugins := pluginmgr.New(dir, filepath.Join(filepath.Dir(exe), "plugins"))
	if err := plugins.UninstallReady(ctx); err != nil {
		return 3
	}
	return 0
}
