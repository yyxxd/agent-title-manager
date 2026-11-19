package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/yyxxd/agent-title-manager/internal/update"
)

const defaultReleaseURL = "https://api.github.com/repos/yyxxd/agent-title-manager/releases/latest"

// updateController keeps the background check result and drives the
// verified download + helper handoff. The public key is injected at build
// time; without it the update page is explicitly disabled.
type updateController struct {
	mu         sync.Mutex
	dir        string
	dataDir    string
	current    string
	publicKey  string
	releaseURL string
	source     *update.Source
	latest     update.Check
	enabled    bool
	busy       bool
	emit       func(string, any)
	quit       func()
	launch     func(string, string) error
}

// NewUpdateController constructs the update controller for a desktop service.
func NewUpdateController(dir, dataDir, current, publicKey, releaseURL string) (*updateController, error) {
	c := &updateController{dir: dir, dataDir: dataDir, current: current,
		publicKey: publicKey, releaseURL: releaseURL}
	if releaseURL == "" {
		c.releaseURL = defaultReleaseURL
	}
	if publicKey == "" {
		return c, nil
	}
	source, err := update.NewSource(c.releaseURL, publicKey)
	if err != nil {
		return nil, err
	}
	c.source, c.enabled = source, true
	return c, nil
}

type updateView struct {
	Current   string        `json:"current"`
	Enabled   bool          `json:"enabled"`
	Available *update.Check `json:"available,omitempty"`
	Error     string        `json:"error,omitempty"`
	Busy      bool          `json:"busy"`
}

// reportUpdateStatus surfaces the helper's result through the existing toast
// channel after a restart, then consumes the status file.
func reportUpdateStatus(app *application.App, service *Service) {
	if service.updater == nil {
		return
	}
	status, err := update.ReadStatus(service.updater.dataDir)
	if err != nil || status.Message == "" {
		return
	}
	application.InvokeAsync(func() {
		app.Event.Emit("desktop:update-status", status)
	})
}

func (c *updateController) bind(emit func(string, any)) {
	if c == nil {
		return
	}
	c.emit = emit
}

func (c *updateController) snapshot() (updateView, error) {
	if c == nil {
		return updateView{}, errors.New("更新组件未初始化")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	view := updateView{Current: c.current, Enabled: c.enabled, Busy: c.busy}
	if c.enabled && c.latest.Version != "" {
		available := c.latest
		view.Available = &available
	}
	return view, nil
}

func (c *updateController) start(ctx context.Context) {
	if c == nil || !c.enabled {
		return
	}
	go func() {
		// A short delay keeps startup work off the UI's critical path.
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := c.checkNow(ctx); err != nil && !errors.Is(err, update.ErrNoUpdate) {
			log.Printf("后台检查更新失败（静默）: %v", err)
		}
	}()
}

func (c *updateController) refresh(ctx context.Context) (updateView, error) {
	if c == nil {
		return updateView{}, errors.New("更新组件未初始化")
	}
	if !c.enabled {
		return c.snapshot()
	}
	if _, err := c.checkNow(ctx); err != nil && !errors.Is(err, update.ErrNoUpdate) {
		view, _ := c.snapshot()
		return view, err
	}
	return c.snapshot()
}

func (c *updateController) checkNow(ctx context.Context) (update.Check, error) {
	check, err := c.source.CheckLatest(ctx, c.current)
	c.mu.Lock()
	c.latest = update.Check{}
	if err == nil {
		c.latest = check
	}
	c.mu.Unlock()
	return check, err
}

type installRequest struct {
	InstallDir string `json:"installDir"`
}

func (c *updateController) install(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	if c == nil {
		return nil, errors.New("更新组件未初始化")
	}
	if !c.enabled {
		return nil, update.ErrNoTrustRoot
	}
	var request installRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, errors.New("安装参数无效")
	}
	c.mu.Lock()
	check, busy := c.latest, c.busy
	if check.Version == "" {
		c.mu.Unlock()
		return nil, update.ErrNoUpdate
	}
	if busy {
		c.mu.Unlock()
		return nil, errors.New("已有更新正在进行")
	}
	c.busy = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.busy = false; c.mu.Unlock() }()

	installer, err := c.download(ctx, check)
	if err != nil {
		return nil, err
	}
	installDir := request.InstallDir
	if installDir == "" {
		installDir, err = detectInstallDir()
		if err != nil {
			return nil, err
		}
	}
	if err := c.handoff(check, installer, installDir); err != nil {
		return nil, err
	}
	return map[string]any{"started": true}, nil
}

func (c *updateController) download(ctx context.Context, check update.Check) (string, error) {
	dir := filepath.Join(c.dataDir, "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".installer-*")
	if err != nil {
		return "", err
	}
	defer file.Close()
	progress := func(done, total int64) {
		if c.emit != nil {
			c.emit("desktop:update-progress", map[string]any{"downloaded": done, "total": total})
		}
	}
	if err := c.source.Download(ctx, check, file, progress); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	// Verify the final filename is the signed one before handoff.
	final := filepath.Join(dir, check.Installer)
	if err := os.Rename(file.Name(), final); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return final, nil
}

func (c *updateController) handoff(check update.Check, installer, installDir string) error {
	pid := os.Getpid()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	handoff := update.Handoff{AppPID: uint32(pid), Installer: installer, InstallDir: installDir,
		DataDir: c.dataDir, Version: check.Version, Restart: exe, Isolated: os.Getenv("ATM_UPDATE_ISOLATED") == "1"}
	path := filepath.Join(c.dataDir, "update-handoff.json")
	data, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	helper := filepath.Join(c.dir, "atm-update.exe")
	// Run from a copy in the data dir so NSIS can replace the installed helper.
	copyPath := filepath.Join(c.dataDir, "updates", "atm-update.exe")
	if err := copyFile(helper, copyPath); err != nil {
		return fmt.Errorf("准备更新 helper 失败: %w", err)
	}
	launch := c.launch
	if launch == nil {
		launch = func(binary, param string) error {
			cmd := exec.Command(binary, param)
			cmd.SysProcAttr = helperProcAttr()
			return cmd.Start()
		}
	}
	return launch(copyPath, path)
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

