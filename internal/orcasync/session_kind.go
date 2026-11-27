package orcasync

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

func botTitlePlace(ctx context.Context, run runCLI, at place) (place, error) {
	id := strings.TrimPrefix(at.ID, idPrefix)
	if at.Kind != "chat" || !nativeID.MatchString(id) || !botFolder(ctx, id, at.Agent) {
		return at, nil
	}
	chats, complete := localBotChats(ctx, run, func(candidate string) bool { return candidate == id })
	match := chats[id]
	if !complete || match.name == "" {
		return place{}, errors.Join(ErrUnavailable, errors.New("无法核对 Bot 话题标题"))
	}
	at.Title = match.title
	if at.Title == nil {
		empty := ""
		at.Title = &empty
	}
	return at, nil
}

func botFolder(ctx context.Context, id, agent string) bool {
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	domain := orcaDatabase()
	records, err := openOrcaDatabase(domain)
	if err != nil {
		return false
	}
	defer records.Close()
	var folder bool
	if err := records.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_session_records WHERE session_id=?
		AND json_extract(record_json,'$.schemaVersion')=2 AND json_extract(record_json,'$.sessionId')=session_id
		AND json_extract(record_json,'$.location.executionHostId')='local'
		AND json_extract(record_json,'$.provider')=? AND json_extract(record_json,'$.location.workspaceKind')='folder')`, id, agent).
		Scan(&folder); err != nil || !folder {
		return false
	}
	return botOwnsConversation(ctx, domain, id)
}

func botOwnsConversation(ctx context.Context, domain, id string) bool {
	bots, err := openOrcaDatabase(filepath.Join(filepath.Dir(domain), "orchestration.db"))
	if err != nil {
		return false
	}
	defer bots.Close()
	var bot bool
	err = bots.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bot_conversation_acts_for WHERE session_id=?
		OR session_id=?)`, id, idPrefix+id).Scan(&bot)
	return err == nil && bot
}

func codexSessionKind(ctx context.Context, id string) string {
	path := orcaDatabase()
	if path == "" || !nativeID.MatchString(id) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	if botOwnsConversation(ctx, path, id) {
		return "bot"
	}
	records, err := openOrcaDatabase(path)
	if err != nil {
		return ""
	}
	defer records.Close()
	var project bool
	if err := records.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_session_records WHERE session_id=?
		AND json_extract(record_json,'$.schemaVersion')=2
		AND json_extract(record_json,'$.provider')='codex'
		AND json_extract(record_json,'$.location.workspaceKind')='git-worktree')`, id).Scan(&project); err == nil && project {
		return "project"
	}
	return ""
}
