package orcasync

import (
	"context"
	"path/filepath"
	"time"
)

func codexSessionKind(ctx context.Context, id string) string {
	path := orcaDatabase()
	if path == "" || !nativeID.MatchString(id) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	bots, err := openOrcaDatabase(filepath.Join(filepath.Dir(path), "orchestration.db"))
	if err != nil {
		return ""
	}
	defer bots.Close()
	var bot bool
	if err := bots.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bot_conversation_acts_for WHERE session_id=?
		OR session_id=?)`, id, idPrefix+id).Scan(&bot); err != nil {
		return ""
	}
	if bot {
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
