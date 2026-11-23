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
	var bots map[string]botChat
	wg.Go(func() { _ = call(ctx, run, &local, "agent", "list") })
	wg.Go(func() { bots, _ = localBotChats(ctx, run, func(id string) bool { _, ok := allowed[id]; return ok }) })
	wg.Wait()
	for id, chat := range bots {
		if chat.name != "" && i.Active(ctx, allowed[id].Agent) {
			names[id] = engine.RecordName{Kind: "bot", Name: chat.name}
		}
	}
	for _, candidate := range local.Agents {
		id := strings.TrimPrefix(candidate.ID, idPrefix)
		key, ok := allowed[id]
		if !ok || key.Kind == "bot" || candidate.Agent != key.Agent || candidate.HostID != "local" ||
			!i.Active(ctx, key.Agent) || !validRecordName(candidate.Repo) {
			continue
		}
		if _, bot := bots[id]; !bot {
			names[id] = engine.RecordName{Kind: "project", Name: candidate.Repo}
		}
	}
	return names
}

type botChat struct {
	name  string
	title *string
}

func localBotChats(ctx context.Context, run runCLI, allowed func(string) bool) (map[string]botChat, bool) {
	var roster struct {
		Bots []struct {
			ID, Name string
		}
	}
	if call(ctx, run, &roster, "bot", "list") != nil || len(roster.Bots) > 12 {
		return nil, false
	}
	var namesMu sync.Mutex
	var workers sync.WaitGroup
	chats := make(map[string]botChat)
	complete := true
	for _, bot := range roster.Bots {
		if !sessionID.MatchString(bot.ID) || !validRecordName(bot.Name) {
			namesMu.Lock()
			complete = false
			namesMu.Unlock()
			continue
		}
		workers.Go(func() {
			var history struct {
				Chats []struct {
					ID    string
					Title *string
				}
			}
			if call(ctx, run, &history, "bot", "chats", "--bot", bot.ID, "--all") != nil {
				namesMu.Lock()
				complete = false
				namesMu.Unlock()
				return
			}
			namesMu.Lock()
			defer namesMu.Unlock()
			for _, chat := range history.Chats {
				id := strings.TrimPrefix(chat.ID, idPrefix)
				if !allowed(id) {
					continue
				}
				if _, found := chats[id]; found {
					chats[id] = botChat{}
				} else {
					chats[id] = botChat{name: bot.Name, title: chat.Title}
				}
			}
		})
	}
	workers.Wait()
	return chats, complete
}

func validRecordName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 160 &&
		!strings.ContainsFunc(name, unicode.IsControl)
}
