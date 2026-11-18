package desktop

import (
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed icon.png
var icon []byte

const appName = "Agent Title Manager"

func Run(dir string, assets fs.FS, service *Service) error {
	var primary atomic.Pointer[application.WebviewWindow]
	app := application.New(application.Options{
		Name:     appName,
		Icon:     icon,
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Services: []application.Service{application.NewService(service)},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			WebviewUserDataPath:           filepath.Join(dir, "WebView2"),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.yyxxd.agent-title-manager" + service.scope,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if window := primary.Load(); window != nil && !slices.Contains(data.Args, "--background") {
					show(window)
				}
			},
		},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: appName, Width: 1080, Height: 780, MinWidth: 450, MinHeight: 450,
		URL: "/", Hidden: slices.Contains(os.Args[1:], "--background"),
		BackgroundColour: application.NewRGB(23, 23, 25),
	})
	primary.Store(window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		window.Hide()
	})
	service.quit = app.Quit
	setupTray(app, window)
	return app.Run()
}

func show(window *application.WebviewWindow) {
	window.Show()
	window.Restore()
	window.Focus()
}

func setupTray(app *application.App, window *application.WebviewWindow) {
	menu := application.NewMenu()
	menu.Add("打开 Agent Title Manager").OnClick(func(_ *application.Context) { show(window) })
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(_ *application.Context) { app.Quit() })
	tray := app.SystemTray.New().SetIcon(icon).SetMenu(menu)
	tray.SetTooltip(appName)
	tray.OnClick(func() { show(window) })
}
