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
	if agent == "antigravity" {
		if nativeHandle != sessionId {
			return place{}, false
		}
		return boundAntigravity(ctx, run, sessionId, id)
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
	if agent == "pi" {
		if terminal, terminalErr := observeTerminal(ctx, run, id, agent); terminalErr == nil {
			return terminal, true
		}
	}
	at, err := showChat(ctx, run, id)
	return at, err == nil && at.Agent == agent
}

func boundAntigravity(ctx context.Context, run runCLI, native, id string) (place, bool) {
	for i, delay := range []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond} {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return place{}, false
			case <-time.After(delay):
			}
		}
		timeout := 1500 * time.Millisecond
		if i == 0 {
			timeout = 900 * time.Millisecond
		}
		probe, cancel := context.WithTimeout(ctx, timeout)
		bound := hasProviderMapping(probe, run, native, id)
		cancel()
		if !bound {
			continue
		}
		probe, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
		at, err := showAgentPlace(probe, run, id)
		if err == nil && at.Agent == "antigravity" {
			if at.Kind == "terminal" {
				at, err = terminalLayout(probe, run, id, at)
			}
			cancel()
			if err == nil && at.Title != nil {
				return at, true
			}
		} else {
			cancel()
		}
	}
	return place{}, false
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
	file, err := openPiSession(ctx, id, handle)
	if err != nil {
		return false
	}
	file.Close()
	return true
}

func openPiSession(ctx context.Context, id, handle string) (*os.File, error) {
	if ctx.Err() != nil || !nativeID.MatchString(id) || !filepath.IsAbs(handle) ||
		!strings.HasSuffix(filepath.Base(handle), "_"+id+".jsonl") {
		return nil, ErrNativePending
	}
	piHome := os.Getenv("PI_CODING_AGENT_DIR")
	if piHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		piHome = filepath.Join(home, ".pi", "agent")
	}
	sessionRoot := filepath.Join(piHome, "sessions")
	rel, err := filepath.Rel(sessionRoot, handle)
	if err != nil || !filepath.IsAbs(sessionRoot) || !filepath.IsLocal(rel) {
		return nil, ErrNativePending
	}
	root, err := os.OpenRoot(sessionRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrNativePending
	}
	file, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	header, err := readPiHeader(file)
	if err != nil || header.Type != "session" || header.ID != id {
		file.Close()
		return nil, ErrNativePending
	}
	return file, nil
}

func readFirstLine(file *os.File) ([]byte, error) {
	return bufio.NewReader(io.LimitReader(file, 64<<10)).ReadBytes('\n')
}
