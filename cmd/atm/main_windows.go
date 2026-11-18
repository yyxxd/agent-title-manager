package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yyxxd/agent-title-manager/frontend"
	"github.com/yyxxd/agent-title-manager/internal/desktop"
	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/ipc"
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
	"golang.org/x/sys/windows"
)

func main() {
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
		core, err := engine.Open(dir, plugins.Sync)
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
