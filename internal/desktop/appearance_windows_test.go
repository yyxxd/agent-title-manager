package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppearanceDefaultsAndPrepaint(t *testing.T) {
	for _, fixture := range []string{"new", "upgrade", "invalid"} {
		t.Run(fixture, func(t *testing.T) {
			dir := t.TempDir()
			if fixture == "upgrade" {
				if err := os.Mkdir(filepath.Join(dir, "WebView2"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if fixture == "invalid" {
				if err := os.WriteFile(filepath.Join(dir, "appearance.txt"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, theme := range []string{"dark", "light"} {
				p := loadAppearance(dir, theme == "dark")
				expectAppearance(t, p, "system", theme)
				handler := p.initialPage([]byte(`<html lang="zh-CN"><body></body></html>`))(http.NotFoundHandler())
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-theme="`+theme+`"`) {
					t.Fatalf("initial document did not select %s: %s", theme, response.Body)
				}
			}
		})
	}
}

func TestAppearancePersistsManualChoiceAndResumesSystem(t *testing.T) {
	dir := t.TempDir()
	p := loadAppearance(dir, false)
	set := func(mode string) {
		t.Helper()
		if _, err := p.set(json.RawMessage(`{"mode":"` + mode + `"}`)); err != nil {
			t.Fatal(err)
		}
	}
	p.onSystemChange(true)
	expectAppearance(t, p, "system", "dark")
	set("light")
	p.onSystemChange(false)
	p.onSystemChange(true)
	expectAppearance(t, p, "light", "light")
	p = loadAppearance(dir, true)
	expectAppearance(t, p, "light", "light")
	set("dark")
	p = loadAppearance(dir, false)
	expectAppearance(t, p, "dark", "dark")
	set("system")
	expectAppearance(t, p, "system", "light")
	p.onSystemChange(true)
	expectAppearance(t, p, "system", "dark")
	if _, err := p.set(json.RawMessage(`{"mode":"invalid"}`)); err == nil {
		t.Fatal("accepted invalid mode")
	}
	expectAppearance(t, loadAppearance(dir, false), "system", "light")
}

func expectAppearance(t *testing.T, p *appearance, mode, theme string) {
	t.Helper()
	if state := p.snapshot(); state.Mode != mode || state.Theme != theme {
		t.Fatalf("wanted %s/%s, got %+v", mode, theme, state)
	}
}
