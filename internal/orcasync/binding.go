package orcasync

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	nativeID  = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	addressID = regexp.MustCompile(`^Address it as orca_session_id:([A-Za-z0-9][A-Za-z0-9_-]{0,127})\.$`)
)

// ApproveBinding verifies an adapter's claimed native-to-Orca mapping without
// reading titles from private Orca storage or accepting an adapter's guess.
func ApproveBinding(ctx context.Context, agent, sessionId, nativeHandle, orcaID string) bool {
	return approveBinding(ctx, localCLI, agent, sessionId, nativeHandle, orcaID)
}

func approveBinding(ctx context.Context, run runCLI, agent, sessionId, nativeHandle, orcaID string) bool {
	_, bound := boundChat(ctx, run, agent, sessionId, nativeHandle, orcaID)
	return bound
}

func boundChat(ctx context.Context, run runCLI, agent, sessionId, nativeHandle, orcaID string) (place, bool) {
	id := strings.TrimPrefix(orcaID, idPrefix)
	if !sessionID.MatchString(id) || !nativeID.MatchString(sessionId) {
		return place{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	switch agent {
	case "codex", "claude":
		if nativeHandle != sessionId {
			return place{}, false
		}
	case "pi":
		if !validPiSession(ctx, sessionId, nativeHandle) {
			return place{}, false
		}
	default:
		return place{}, false
	}
	if !hasProviderMapping(ctx, run, nativeHandle, id) {
		return place{}, false
	}
	at, err := showChat(ctx, run, id)
	return at, err == nil && at.Agent == agent
}

func hasProviderMapping(ctx context.Context, run runCLI, handle, id string) bool {
	wire, _ := run(ctx, "agent", "show", handle, "--environment", "local", "--json")
	var diagnostic struct {
		OK    *bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(wire, &diagnostic) != nil || diagnostic.OK == nil || *diagnostic.OK ||
		diagnostic.Error.Code != "agent_provider_id" {
		return false
	}
	message := strings.TrimSpace(diagnostic.Error.Message)
	i := strings.LastIndex(message, "Address it as ")
	if i < 0 {
		return false
	}
	result := addressID.FindStringSubmatch(message[i:])
	return len(result) == 2 && result[1] == id
}

func validPiSession(ctx context.Context, id, handle string) bool {
	if ctx.Err() != nil || !filepath.IsAbs(handle) ||
		!strings.HasSuffix(filepath.Base(handle), "_"+id+".jsonl") {
		return false
	}
	piHome := os.Getenv("PI_CODING_AGENT_DIR")
	if piHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		piHome = filepath.Join(home, ".pi", "agent")
	}
	sessionRoot := filepath.Join(piHome, "sessions")
	rel, err := filepath.Rel(sessionRoot, handle)
	if err != nil || !filepath.IsAbs(sessionRoot) || !filepath.IsLocal(rel) {
		return false
	}
	root, err := os.OpenRoot(sessionRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer file.Close()
	line, err := bufio.NewReader(io.LimitReader(file, 64<<10)).ReadBytes('\n')
	var header struct{ Type, ID string }
	return err == nil && json.Unmarshal(line, &header) == nil && header.Type == "session" && header.ID == id
}
