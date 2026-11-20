package orcasync

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	titleAuto    = "auto"
	titleManual  = "manual"
	titleUnknown = "unknown"
)

var defaultTitles = map[string]string{"codex": "Codex Chat", "claude": "Claude Chat", "pi": "Pi Chat", "antigravity": "Antigravity Chat"}

func resolvePlaceOrigin(ctx context.Context, at place, database func(context.Context, string, string, string) string) string {
	if at.Kind == "terminal" {
		return terminalOrigin(at, at.Agent)
	}
	return database(ctx, at.Agent, strings.TrimPrefix(at.ID, idPrefix), *at.Title)
}

func terminalOrigin(at place, agent string) string {
	title := ""
	if at.Title != nil {
		title = *at.Title
	}
	if strings.TrimSpace(title) == "" {
		return titleAuto
	}
	common := map[string]bool{"terminal": true, "pwsh": true, "pwsh.exe": true, "powershell": true, "powershell.exe": true, "cmd": true, "cmd.exe": true, "bash": true, "zsh": true, "sh": true, "agy": true, "agy.exe": true, "antigravity": true, "pi": true, "pi.exe": true}
	base := strings.ToLower(filepath.Base(title))
	if title == at.PaneTitle || common[base] || base == strings.ToLower(agent) || base == strings.ToLower(agent)+".exe" {
		return titleAuto
	}
	return titleUnknown
}

func orcaDatabase() string {
	dir := os.Getenv("ORCA_USER_DATA_PATH")
	if dir == "" {
		if roaming := os.Getenv("APPDATA"); filepath.IsAbs(roaming) {
			dir = filepath.Join(roaming, "Orca")
		}
	}
	if !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Join(dir, "agent-session-domain.db")
}

func databaseURI(file string, query url.Values) string {
	path := filepath.ToSlash(file)
	if filepath.VolumeName(file) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

type nameRecord struct {
	schema               sql.NullInt64
	session, provider    sql.NullString
	nameType, sourceType sql.NullString
	name, source         sql.NullString
}

func titleOrigin(ctx context.Context, path, agent, id, displayed string) string {
	db, err := openOrcaDatabase(path)
	if err != nil {
		return titleUnknown
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	record, ok := readNameRecord(ctx, db, id)
	if !ok || record.schema.Int64 != 2 || record.session.String != id || record.provider.String != agent {
		return titleUnknown
	}
	return record.origin(agent, displayed)
}

func readNameRecord(ctx context.Context, db *sql.DB, id string) (nameRecord, bool) {
	var r nameRecord
	rows, err := db.QueryContext(ctx, `
		SELECT CASE WHEN json_type(record_json, '$.schemaVersion') = 'integer' THEN json_extract(record_json, '$.schemaVersion') END,
		       CASE WHEN json_type(record_json, '$.sessionId') = 'text' THEN json_extract(record_json, '$.sessionId') END,
		       CASE WHEN json_type(record_json, '$.provider') = 'text' THEN json_extract(record_json, '$.provider') END,
		       json_type(record_json, '$.conversationName'),
		       json_type(record_json, '$.conversationNameSource'),
		       json_extract(record_json, '$.conversationName'),
		       json_extract(record_json, '$.conversationNameSource')
		FROM agent_session_records WHERE session_id = ? LIMIT 2`, id)
	if err != nil {
		return r, false
	}
	defer rows.Close()
	if !rows.Next() {
		return r, false
	}
	err = rows.Scan(&r.schema, &r.session, &r.provider, &r.nameType, &r.sourceType, &r.name, &r.source)
	return r, err == nil && !rows.Next() && rows.Err() == nil
}

func (r nameRecord) origin(agent, displayed string) string {
	if agent == "antigravity" && r.sourceType.String == "text" && r.source.String == "user" {
		return titleManual
	}
	if r.nameType.String == "text" && r.sourceType.String == "text" && r.name.String == displayed {
		if r.source.String == "user" {
			return titleManual
		}
	}
	if agent == "antigravity" && r.nameType.String == "text" && r.name.String != "" && r.name.String == displayed &&
		(!r.sourceType.Valid || r.sourceType.String == "text" && r.source.String == "auto") {
		return titleAuto
	}
	if !r.nameType.Valid && !r.sourceType.Valid && displayed != "" && displayed == defaultTitles[agent] {
		return titleAuto
	}
	return titleUnknown
}
