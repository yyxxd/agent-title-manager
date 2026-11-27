package pluginmgr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func (m *Manager) AntigravityPackage(ctx context.Context) (string, error) {
	return m.observerPackage(ctx, "antigravity")
}

func (m *Manager) CodexPackage(ctx context.Context) (string, error) {
	return m.observerPackage(ctx, "codex")
}

func (m *Manager) observerPackage(ctx context.Context, agent string) (string, error) {
	enabled, err := m.Enabled(ctx, agent)
	if err != nil || !enabled {
		return "", err
	}
	state, err := m.load()
	if err != nil {
		return "", err
	}
	r := state.Items[agent]
	if !r.Enabled || r.Dir == "" || r.Manifest.ID != agent || state.Pending != nil && state.Pending.ID == agent {
		return "", nil
	}
	if !filepath.IsLocal(r.Dir) || filepath.Base(r.Dir) != r.Dir {
		return "", errors.New("Antigravity 插件目录无效")
	}
	dir := m.packageDir(r.release)
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || isReparse(info) {
		return "", errors.New("Antigravity 插件目录无效")
	}
	manifest, err := readManifest(dir)
	if err != nil {
		return "", err
	}
	if manifest != r.Manifest {
		return "", errors.New("观察器插件清单已改变")
	}
	return dir, nil
}

func (m *Manager) DesktopObserverEntry(ctx context.Context) (string, error) {
	dir, err := m.AntigravityPackage(ctx)
	if err != nil || dir == "" {
		return "", err
	}
	entry := filepath.Join(dir, "observe.mjs")
	info, err := os.Lstat(entry)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || isReparse(info) {
		return "", errors.New("Antigravity 观察器路径无效")
	}
	return entry, nil
}

func (m *Manager) NodeCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, node, args...)
	hideProcess(cmd)
	cmd.Env = withoutNodeInjection(cmd.Environ())
	return cmd, nil
}

func (m *Manager) RunDesktopObserver(ctx context.Context, entry string) error {
	cmd, err := m.NodeCommand(ctx, entry)
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	return cmd.Run()
}
