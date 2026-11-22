package desktop

import (
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

//go:embed icon.png
var icon []byte

const appName = "Agent Title Manager"

//go:embed version.txt
var defaultVersion string

var buildVersion string

// updatePublicKey is injected at build time as a hex Ed25519 public key.
// An empty value disables in-app updates and surfaces that in the UI.
var updatePublicKey string

// UpdatePublicKey returns the hex-encoded Ed25519 key injected at build time.
func UpdatePublicKey() string { return updatePublicKey }

func versionedAppName() string {
	return appName + " " + BuildVersion()
}

// BuildVersion returns the version embedded at build time, or the shipped default.
func BuildVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	return strings.TrimSpace(defaultVersion)
}

func Run(dir string, assets fs.FS, service *Service) error {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return err
	}
	preference := loadAppearance(dir, w32.IsCurrentlyDarkMode())
	service.appearance = preference
	var primary atomic.Pointer[application.WebviewWindow]
	app := application.New(application.Options{
		Name:     appName,
		Icon:     icon,
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets), Middleware: preference.initialPage(index)},
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
	initial := preference.snapshot()
	background, foreground := initial.colours()
	windowOptions := appearanceWindowOptions(initial, background, foreground)
	// 组合托管让 Wails 能将网页标题栏交给 Windows 做原生命中检测和贴靠。
	windowOptions.WebView2CompositionHosting = true
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: versionedAppName(), Width: 1080, Height: 780, MinWidth: 450, MinHeight: 450,
		Frameless: true,
		URL:       "/", Hidden: slices.Contains(os.Args[1:], "--background"),
		BackgroundColour: background,
		Windows:          windowOptions,
	})
	primary.Store(window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		window.Hide()
	})
	service.quit = app.Quit
	service.bindEmitter(func(name string, data any) { app.Event.Emit(name, data) })
	service.updater.bind(service.emit)
	reportUpdateStatus(app, service)
	connectAppearance(app, window, service)
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
	tray.SetTooltip(versionedAppName())
	tray.OnClick(func() { show(window) })
}
