package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

type appearance struct {
	mu         sync.Mutex
	path       string
	systemDark bool
	state      appearanceState
}

type appearanceState struct {
	Mode     string `json:"mode"`
	Theme    string `json:"theme"`
	Revision uint64 `json:"revision"`
}

func loadAppearance(dir string, systemDark bool) *appearance {
	p := &appearance{path: filepath.Join(dir, "appearance.txt"), systemDark: systemDark}
	data, err := os.ReadFile(p.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("读取桌面外观: %v", err)
	}
	mode := strings.TrimSpace(string(data))
	if !validAppearance(mode) {
		mode = "system"
	}
	p.state.Mode = mode
	p.resolve()
	return p
}

func validAppearance(mode string) bool { return mode == "system" || mode == "light" || mode == "dark" }

func (p *appearance) resolve() {
	p.state.Theme = "light"
	if p.state.Mode == "dark" || p.state.Mode == "system" && p.systemDark {
		p.state.Theme = "dark"
	}
	p.state.Revision++
}

func (p *appearance) snapshot() appearanceState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *appearance) onSystemChange(dark bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systemDark = dark
	p.resolve()
}

func (p *appearance) set(raw json.RawMessage) (appearanceState, error) {
	var request struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &request); err != nil || !validAppearance(request.Mode) {
		return appearanceState{}, errors.New("请选择跟随系统、浅色或深色")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := saveAppearance(p.path, request.Mode); err != nil {
		return appearanceState{}, errors.New("无法保存外观，请重试")
	}
	p.state.Mode = request.Mode
	p.resolve()
	return p.state, nil
}

func saveAppearance(path, mode string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".appearance-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.WriteString(mode + "\n")
	if err = errors.Join(err, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func (p *appearance) initialPage(index []byte) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.URL.Path != "/" && request.URL.Path != "/index.html" {
				next.ServeHTTP(response, request)
				return
			}
			theme := p.snapshot().Theme
			page := bytes.Replace(index, []byte(`<html lang="zh-CN">`), []byte(`<html lang="zh-CN" data-theme="`+theme+`">`), 1)
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			_, _ = response.Write(page)
		})
	}
}

func (state appearanceState) nativeTheme() application.Theme {
	if state.Theme == "dark" {
		return application.Dark
	}
	return application.Light
}

func (state appearanceState) colours() (application.RGBA, application.RGBA) {
	if state.Theme == "dark" {
		return application.NewRGB(17, 19, 24), application.NewRGB(243, 244, 246)
	}
	return application.NewRGB(245, 246, 248), application.NewRGB(17, 24, 39)
}

func (state appearanceState) apply(window *application.WebviewWindow) {
	background, foreground := state.colours()
	window.SetBackgroundColour(background)
	if handle := window.NativeWindow(); handle != nil && !w32.IsCurrentlyHighContrastMode() {
		hwnd := uintptr(handle)
		w32.SetTheme(hwnd, state.Theme == "dark")
		w32.SetTitleBarColour(hwnd, colourRef(background))
		w32.SetTitleTextColour(hwnd, colourRef(foreground))
	}
}

func colourRef(colour application.RGBA) uint32 {
	return uint32(colour.Red) | uint32(colour.Green)<<8 | uint32(colour.Blue)<<16
}

func appearanceWindowOptions(state appearanceState, background, foreground application.RGBA) application.WindowsWindow {
	caption, text := colourRef(background), colourRef(foreground)
	native := &application.WindowTheme{TitleBarColour: &caption, TitleTextColour: &text}
	return application.WindowsWindow{
		Theme: state.nativeTheme(),
		CustomTheme: application.ThemeSettings{
			LightModeActive: native, LightModeInactive: native,
			DarkModeActive: native, DarkModeInactive: native,
		},
	}
}

func connectAppearance(app *application.App, window *application.WebviewWindow, service *Service) {
	preference := service.appearance
	service.appearanceChanged = func() {
		application.InvokeAsync(func() {
			state := preference.snapshot()
			state.apply(window)
			app.Event.Emit("desktop:appearance", state)
		})
	}
	app.Event.OnApplicationEvent(events.Windows.SystemThemeChanged, func(event *application.ApplicationEvent) {
		preference.onSystemChange(event.Context().IsDarkMode())
		service.appearanceChanged()
	})
}
