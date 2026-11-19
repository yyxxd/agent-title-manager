package engine

import (
	"database/sql"
	"fmt"
	"strings"
)

const schema = `
CREATE TABLE settings (
	id INTEGER PRIMARY KEY CHECK(id=1), provider TEXT NOT NULL DEFAULT 'codex',
	codex_model TEXT NOT NULL DEFAULT 'gpt-6-luna', fast INTEGER NOT NULL DEFAULT 1,
	api_url TEXT NOT NULL DEFAULT '', api_model TEXT NOT NULL DEFAULT '', api_key BLOB,
	fallback INTEGER NOT NULL DEFAULT 0, max_length INTEGER NOT NULL DEFAULT 15,
	since INTEGER NOT NULL
);
INSERT INTO settings(id,since) VALUES(1, CAST(unixepoch('subsec')*1000 AS INTEGER));
CREATE TABLE sessions (
	agent TEXT NOT NULL, session_id TEXT NOT NULL, revision TEXT NOT NULL,
	title TEXT NOT NULL, manual INTEGER NOT NULL, orca_id TEXT NOT NULL DEFAULT '',
	orca_managed TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(agent, session_id)
);
CREATE TABLE events (
	id TEXT PRIMARY KEY, agent TEXT NOT NULL, session_id TEXT NOT NULL, revision TEXT NOT NULL,
	native_only INTEGER NOT NULL DEFAULT 0, base TEXT NOT NULL, title TEXT NOT NULL,
	status TEXT NOT NULL, payload BLOB, orca_id TEXT NOT NULL, orca_base TEXT,
	orca_origin TEXT NOT NULL, sync_status TEXT NOT NULL DEFAULT '',
	created INTEGER NOT NULL, updated INTEGER NOT NULL, finalized INTEGER,
	UNIQUE(agent,session_id,revision,native_only),
	FOREIGN KEY(agent,session_id) REFERENCES sessions(agent,session_id)
);
CREATE INDEX events_queue ON events(status,created);
CREATE INDEX events_sync ON events(sync_status,updated);
CREATE INDEX events_display ON events(updated DESC);
CREATE INDEX events_finalized ON events(finalized) WHERE finalized IS NOT NULL;
CREATE TABLE attempts (
	id INTEGER PRIMARY KEY, event_id TEXT REFERENCES events(id), agent TEXT NOT NULL,
	provider TEXT NOT NULL, model TEXT NOT NULL, source TEXT NOT NULL,
	requested_tier TEXT NOT NULL, actual_tier TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL, started INTEGER NOT NULL, input_tokens INTEGER,
	output_tokens INTEGER, cached_tokens INTEGER
);
CREATE INDEX attempts_started ON attempts(started);
CREATE INDEX attempts_event ON attempts(event_id);
PRAGMA user_version=1;
`

const schemaV2 = `
ALTER TABLE sessions ADD COLUMN project_key TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN turns_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN turn_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN confirmation_skips INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN native_handle TEXT NOT NULL DEFAULT '';
CREATE INDEX sessions_project ON sessions(project_key,title) WHERE project_key<>'';
PRAGMA user_version=2;
`

const schemaV3 = `
ALTER TABLE settings ADD COLUMN api_protocol TEXT NOT NULL DEFAULT 'chat';
ALTER TABLE settings ADD COLUMN api_legacy INTEGER NOT NULL DEFAULT 0;
UPDATE settings SET api_legacy=1 WHERE api_url<>'';
PRAGMA user_version=3;
`

const schemaV4 = `
ALTER TABLE events ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN failure_length INTEGER;
ALTER TABLE events ADD COLUMN failure_limit INTEGER;
PRAGMA user_version=4;
`

const schemaV5 = `
UPDATE settings SET max_length=CASE WHEN max_length<=22 THEN 15 ELSE 30 END
	WHERE id=1 AND max_length NOT IN (15,30);
PRAGMA user_version=5;
`

const schemaV6 = `
ALTER TABLE settings ADD COLUMN codex_effort TEXT NOT NULL DEFAULT 'low';
ALTER TABLE settings ADD COLUMN claude_model TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN claude_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN actual_model TEXT NOT NULL DEFAULT '';
PRAGMA user_version=6;
`

const schemaV7 = `ALTER TABLE events ADD COLUMN sync_error TEXT NOT NULL DEFAULT '';`

func initialize(db *sql.DB) error {
	for _, pragmas := range []string{"PRAGMA busy_timeout=2500", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA secure_delete=ON"} {
		if _, err := db.Exec(pragmas); err != nil {
			return err
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch version {
	case 0:
		if _, err = tx.Exec(schema); err != nil {
			return err
		}
		fallthrough
	case 1:
		if _, err = tx.Exec(schemaV2); err != nil {
			return err
		}
		fallthrough
	case 2:
		if _, err = tx.Exec(schemaV3); err != nil {
			return err
		}
		fallthrough
	case 3:
		if _, err = tx.Exec(schemaV4); err != nil {
			return err
		}
		fallthrough
	case 4:
		if _, err = tx.Exec(schemaV5); err != nil {
			return err
		}
		fallthrough
	case 5:
		if _, err = tx.Exec(schemaV6); err != nil {
			return err
		}
		fallthrough
	case 6:
		if _, err = tx.Exec(schemaV7); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		fallthrough
	case 7:
	default:
		return fmt.Errorf("无法读取数据库版本 %d", version)
	}
	if version == 0 {
		if _, err = tx.Exec("UPDATE settings SET codex_model='',codex_effort='' WHERE id=1"); err != nil {
			return err
		}
	}
	for _, recovery := range []string{
		"UPDATE attempts SET status='interrupted' WHERE status='running'",
		"UPDATE events SET status='interrupted',payload=NULL,updated=CAST(unixepoch('subsec')*1000 AS INTEGER) WHERE status='running'",
		"UPDATE events SET sync_status='pending' WHERE sync_status='writing'",
	} {
		if _, err = tx.Exec(recovery); err != nil {
			return err
		}
	}
	return tx.Commit()
}
