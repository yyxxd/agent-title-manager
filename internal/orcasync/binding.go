package orcasync

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

var (
	nativeID  = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	addressID = regexp.MustCompile(`^Address it as orca_session_id:([A-Za-z0-9][A-Za-z0-9_-]{0,127})\.$`)
)

type NativeHandleValidator func(context.Context, string, string, string) bool

type Binding struct {
	ID, Title, Origin, Handle, Kind string
	Surface                         string
}

func supportsAgent(agent string) bool {
	switch agent {
	case "codex", "claude", "pi", "antigravity":
		return true
	default:
		return false
	}
}

func validLocator(ctx context.Context, validate NativeHandleValidator, agent, session, handle string) bool {
	if agent == "pi" {
		return validate != nil && validate(ctx, agent, session, handle)
	}
	return handle == session
}

func mappedPlace(ctx context.Context, run runCLI, agent, id string) (place, error) {
	at, err := showAgentPlace(ctx, run, id)
	if err != nil {
		return place{}, err
	}
	if at.Agent != agent {
		return place{}, ErrSkipped
	}
	if at.Kind == "terminal" {
		return terminalLayout(ctx, run, id, at)
	}
	if at.Title == nil {
		empty := ""
		at.Title = &empty
	}
	return botTitlePlace(ctx, run, at)
}

func discover(ctx context.Context, run runCLI, validate NativeHandleValidator, agent, session, handle, hint, kind string, delays []time.Duration) (place, bool) {
	rawHint := strings.TrimPrefix(hint, idPrefix)
	if (hint != "" && !sessionID.MatchString(rawHint)) || !supportsAgent(agent) || !nativeID.MatchString(session) {
		return place{}, false
	}
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return place{}, false
			case <-timer.C:
			}
		}
		if !validLocator(ctx, validate, agent, session, handle) {
			continue
		}
		// Bot 话题还需要通过公开的 Bot 列表和话题列表核对标题。
		probe, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		id := lookupProviderMapping(probe, run, handle)
		if id == "" {
			cancel()
			continue
		}
		if rawHint != "" && rawHint != id {
			cancel()
			return place{}, false
		}
		at, err := mappedPlace(probe, run, agent, id)
		cancel()
		if err == nil {
			return at, kind == "" || at.Kind == kind
		}
	}
	return place{}, false
}

func lookupProviderMapping(ctx context.Context, run runCLI, handle string) string {
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
		return ""
	}
	message := strings.TrimSpace(diagnostic.Error.Message)
	i := strings.LastIndex(message, "Address it as ")
	if i < 0 {
		return ""
	}
	result := addressID.FindStringSubmatch(message[i:])
	if len(result) == 2 {
		return result[1]
	}
	return ""
}
