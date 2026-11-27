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
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(desktop.BuildVersion())
		return
	}
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
		plugins.SetBuildVersion(desktop.BuildVersion())
		orca := orcaIntegration(plugins)
		core, err := engine.Open(dir, syncer(orca), func(ctx context.Context, id string) bool {
			enabled, err := plugins.Enabled(ctx, id)
			return err == nil && enabled
		})
		if err != nil {
			return desktop.Backend{}, err
		}
		return desktop.Backend{
			Engine: core.Handle, Plugins: plugins.Handle, Enabled: plugins.Enabled,
			ObserveCodexHook:   plugins.ObserveCodexHook,
			AutoUpdateBuiltins: func(ctx context.Context) (any, error) { return plugins.AutoUpdateBuiltins(ctx) },
			OrcaEnabled:        orca.Active, ResolveTitleOrigin: orca.ResolveTitleOrigin,
			RetryBinding: orca.RetryBinding, NativeTitleMatches: orca.NativeTitleMatches,
			ReadProtectionTitles: orca.ReadProtectionTitles,
			RecordNames:          orca.RecordNames,
			AttachBinding:        core.AttachBinding, Close: core.Close,
			Observe: func(ctx context.Context) {
				desktop.RunObservers(ctx, plugins.Changes,
					desktop.Observer{Name: "Antigravity Desktop", Select: plugins.DesktopObserverEntry, Run: plugins.RunDesktopObserver},
					desktop.Observer{Name: "Orca Codex", Select: func(ctx context.Context) (string, error) {
						if !orca.Active(ctx, "codex") {
							return "", nil
						}
						return plugins.CodexPackage(ctx)
					}, Run: func(ctx context.Context, dir string) error {
						return orca.RunCodexObserver(ctx, dir, plugins.NodeCommand, core)
					}},
					desktop.Observer{Name: "Orca Chat", Select: func(ctx context.Context) (string, error) {
						if !orca.Active(ctx, "antigravity") {
							return "", nil
						}
						return plugins.AntigravityPackage(ctx)
					}, Run: func(ctx context.Context, dir string) error {
						return orca.RunACPObserver(ctx, dir, plugins.NodeCommand, core.Handle)
					}},
				)
			},
		}, nil
	})
	// NewUpdateController(appDir, dataDir, ...): helper lives beside the app;
	// downloads and the handoff file live in the isolated data dir.
	updater, err := desktop.NewUpdateController(filepath.Dir(exe), dir, desktop.BuildVersion(),
		desktop.UpdatePublicKey(), os.Getenv("ATM_UPDATE_URL"))
	if err != nil {
		return err
	}
	service.SetUpdater(updater)
	return desktop.Run(dir, frontend.Assets(), service)
}

func orcaIntegration(plugins *pluginmgr.Manager) *orcasync.Integration {
	return &orcasync.Integration{
		Enabled: func(ctx context.Context, agent string) (bool, error) {
			on, err := plugins.IntegrationEnabled(ctx, pluginmgr.OrcaIntegration)
			if err != nil || !on {
				return false, err
			}
			return plugins.Enabled(ctx, agent)
		},
		Changes: plugins.Changes, Validate: plugins.ValidateNativeHandle,
		ReadNative: plugins.ReadNativeTitle, ReadNativeAt: plugins.ReadNativeTitleAt,
	}
}

func syncer(orca *orcasync.Integration) func(context.Context, string, string, string) error {
	return func(ctx context.Context, id, expected, title string) error {
		target, ok := engine.SyncTargetFromContext(ctx)
		if !ok {
			return orcasync.ErrNativePending
		}
		if target.ExpectedTitle != title {
			return engine.ErrSyncStale
		}
		if !orca.Active(ctx, target.Agent) {
			return engine.ErrSyncSkipped
		}
		err := orca.SyncVerified(ctx, id, expected, title, target.Agent, target.SessionID, target.NativeHandle)
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
