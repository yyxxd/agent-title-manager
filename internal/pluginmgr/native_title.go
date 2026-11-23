package pluginmgr

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

//go:embed read-native-title.mjs
var nativeTitleScript string

var ErrNativePending = errors.New("暂时无法读取原生会话标题")

func (m *Manager) ReadNativeTitle(ctx context.Context, agent, sessionID, handle string) (string, error) {
	enabled, err := m.Enabled(ctx, agent)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", ErrNativePending
	}
	if agent == "pi" {
		return ReadPiTitle(ctx, sessionID, handle)
	}
	node, adapterDir, hostDir, err := m.nativeReader(agent, sessionID, handle)
	if err != nil {
		return "", err
	}
	return adapterTitle(ctx, node, agent, sessionID, adapterDir, hostDir)
}

func (m *Manager) nativeReader(agent, sessionID, handle string) (string, string, string, error) {
	if agent != "codex" && agent != "claude" && agent != "antigravity" {
		return "", "", "", ErrNativePending
	}
	if agent == "antigravity" && sessionID != handle {
		return "", "", "", ErrNativePending
	}
	state, err := m.load()
	if err != nil {
		return "", "", "", err
	}
	r := state.Items[agent]
	if !r.Enabled || r.Dir == "" || r.Manifest.ID != agent || state.Pending != nil && state.Pending.ID == agent {
		return "", "", "", ErrNativePending
	}
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		return "", "", "", err
	}
	h, err := resolveHost(agent)
	if err != nil {
		return "", "", "", err
	}
	return node, m.packageDir(r.release), h.dir, nil
}

func (m *Manager) ReadAntigravityTitleAt(ctx context.Context, id, root string) (string, error) {
	dir, err := m.AntigravityPackage(ctx)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", ErrNativePending
	}
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		return "", err
	}
	return adapterTitle(ctx, node, "antigravity", id, dir, "", root)
}

func adapterTitle(ctx context.Context, node, agent, sessionID, adapterDir, hostDir string, scope ...string) (string, error) {
	wire, err := adapterOutput(ctx, node, agent, sessionID, adapterDir, hostDir, scope...)
	if err != nil {
		return "", err
	}
	return decodeAdapterTitle(wire)
}

func adapterOutput(ctx context.Context, node, agent, sessionID, adapterDir, hostDir string, scope ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, append([]string{"--input-type=module", "-e", nativeTitleScript, agent, sessionID, adapterDir, hostDir}, scope...)...)
	hideProcess(cmd)
	cmd.Env = withoutNodeInjection(cmd.Environ())
	wire, err := cmd.Output()
	if err != nil {
		return nil, ErrNativePending
	}
	return wire, nil
}

func decodeAdapterTitle(wire []byte) (string, error) {
	var reply struct {
		Title *string `json:"title"`
	}
	if len(wire) > 8192 || json.Unmarshal(wire, &reply) != nil || reply.Title == nil {
		return "", errors.New("原生适配器未返回已核验标题")
	}
	return *reply.Title, nil
}

func withoutNodeInjection(env []string) []string {
	out := env[:0]
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		if !strings.EqualFold(name, "NODE_OPTIONS") && !strings.EqualFold(name, "NODE_PATH") {
			out = append(out, item)
		}
	}
	return out
}
