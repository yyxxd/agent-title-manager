package orcasync

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func (i *Integration) RecordNames(ctx context.Context, identities []engine.RecordIdentity) map[string]engine.RecordName {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	allowed := make(map[string]engine.RecordIdentity)
	var agent string
	for _, key := range identities {
		if sessionID.MatchString(key.OrcaID) && i.Active(ctx, key.Agent) {
			allowed[key.OrcaID] = key
			agent = key.Agent
		}
	}
	if agent == "" {
		return nil
	}
	names := make(map[string]engine.RecordName)
	var local struct {
		Agents []place `json:"agents"`
	}
	run := i.command(agent)
	var wg sync.WaitGroup
	var bots map[string]string
	wg.Go(func() { _ = call(ctx, run, &local, "agent", "list") })
	wg.Go(func() { bots = localBotNames(ctx, run, allowed) })
	wg.Wait()
	for id, name := range bots {
		if i.Active(ctx, allowed[id].Agent) {
			names[id] = engine.RecordName{Kind: "bot", Name: name}
		}
	}
	for _, candidate := range local.Agents {
		id := strings.TrimPrefix(candidate.ID, idPrefix)
		key, ok := allowed[id]
		if !ok || key.Kind == "bot" || candidate.Agent != key.Agent || candidate.HostID != "local" ||
			!i.Active(ctx, key.Agent) || !validRecordName(candidate.Repo) {
			continue
		}
		if _, bot := names[id]; !bot {
			names[id] = engine.RecordName{Kind: "project", Name: candidate.Repo}
		}
	}
	return names
}

func localBotNames(ctx context.Context, run runCLI, allowed map[string]engine.RecordIdentity) map[string]string {
	var roster struct {
		Bots []struct {
			ID, Name string
		}
	}
	if call(ctx, run, &roster, "bot", "list") != nil || len(roster.Bots) > 12 {
		return nil
	}
	var namesMu sync.Mutex
	var workers sync.WaitGroup
	names := make(map[string]string)
	owners := make(map[string]string)
	for _, bot := range roster.Bots {
		if !sessionID.MatchString(bot.ID) || !validRecordName(bot.Name) {
			continue
		}
		workers.Go(func() {
			var history struct {
				Chats []struct{ ID string }
			}
			if call(ctx, run, &history, "bot", "chats", "--bot", bot.ID, "--all") != nil {
				return
			}
			namesMu.Lock()
			defer namesMu.Unlock()
			for _, chat := range history.Chats {
				id := strings.TrimPrefix(chat.ID, idPrefix)
				if _, ok := allowed[id]; !ok {
					continue
				}
				if prior, found := owners[id]; found && prior != bot.ID {
					delete(names, id)
				} else {
					owners[id], names[id] = bot.ID, bot.Name
				}
			}
		})
	}
	workers.Wait()
	return names
}

func validRecordName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 160 &&
		!strings.ContainsFunc(name, unicode.IsControl)
}
