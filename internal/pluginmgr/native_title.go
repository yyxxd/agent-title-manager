package pluginmgr

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

//go:embed read-native-title.mjs
var nativeTitleScript string

func (m *Manager) readNativeTitle(ctx context.Context, agent, sessionID, handle string) (string, error) {
	enabled, err := m.Enabled(ctx, agent)
	if err != nil || !enabled {
		return "", orcasync.ErrNativePending
	}
	if agent == "pi" {
		return orcasync.ReadPiTitle(ctx, sessionID, handle)
	}
	if agent != "codex" && agent != "claude" {
		return "", orcasync.ErrNativePending
	}
	state, err := m.load()
	if err != nil {
		return "", err
	}
	r := state.Items[agent]
	if !r.Enabled || r.Dir == "" || r.Manifest.ID != agent || state.Pending != nil && state.Pending.ID == agent {
		return "", orcasync.ErrNativePending
	}
	node, err := m.findExecutable("node.exe", true)
	if err != nil {
		return "", err
	}
	h, err := resolveHost(agent)
	if err != nil {
		return "", err
	}
	return adapterTitle(ctx, node, agent, sessionID, m.packageDir(r.release), h.dir)
}

func adapterTitle(ctx context.Context, node, agent, sessionID, adapterDir, hostDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", nativeTitleScript, agent, sessionID, adapterDir, hostDir)
	hideProcess(cmd)
	cmd.Env = withoutNodeInjection(cmd.Environ())
	wire, err := cmd.Output()
	if err != nil {
		return "", orcasync.ErrNativePending
	}
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
