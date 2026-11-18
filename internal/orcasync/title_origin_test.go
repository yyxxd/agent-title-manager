package orcasync

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTitleOriginDistinguishesIdenticalVisibleTitles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "with spaces # 家.db")
	db, err := sql.Open("sqlite", databaseURI(file, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE agent_session_records (session_id TEXT PRIMARY KEY, record_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, fields, displayed, want string
	}{
		{"pi_auto", "", "Pi Chat", "auto"},
		{"pi_manual", `,"conversationName":"Pi Chat","conversationNameSource":"user"`, "Pi Chat", "manual"},
		{"pi_moved", `,"conversationName":"旧名称","conversationNameSource":"user"`, "Pi Chat", "unknown"},
		{"pi_ambiguous", `,"conversationName":"Pi Chat"`, "Pi Chat", "unknown"},
		{"pi_null", `,"conversationName":null`, "Pi Chat", "unknown"},
		{"pi_other", "", "其他", "unknown"},
	} {
		doc := fmt.Sprintf(`{"schemaVersion":2,"sessionId":"%s","provider":"pi"%s}`, c.id, c.fields)
		if _, err := db.Exec(`INSERT INTO agent_session_records VALUES (?, ?)`, c.id, doc); err != nil {
			t.Fatal(err)
		}
		if got := titleOrigin(context.Background(), file, "pi", c.id, c.displayed); got != c.want {
			t.Errorf("%s: got %s; want %s", c.id, got, c.want)
		}
	}
}

func TestTitleOriginRejectsUnknownIdentityAndSchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "missing.db")
	if got := titleOrigin(context.Background(), file, "codex", "chat", "Codex Chat"); got != "unknown" {
		t.Fatal(got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("missing database was touched: %v", err)
	}
	db, err := sql.Open("sqlite", databaseURI(file, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE agent_session_records (session_id TEXT PRIMARY KEY, record_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{
		`{"schemaVersion":3,"sessionId":"chat","provider":"codex"}`,
		`{"schemaVersion":"2","sessionId":"chat","provider":"codex"}`,
		`{"schemaVersion":2,"sessionId":"other","provider":"codex"}`,
		`{"schemaVersion":2,"sessionId":"chat","provider":"pi"}`,
		`{"schemaVersion":`,
	} {
		if _, err := db.Exec(`INSERT OR REPLACE INTO agent_session_records VALUES ('chat', ?)`, doc); err != nil {
			t.Fatal(err)
		}
		if got := titleOrigin(context.Background(), file, "codex", "chat", "Codex Chat"); got != "unknown" {
			t.Fatalf("unsafe origin for fixture %s: %s", doc, got)
		}
	}
}

func TestInstalledOrcaPiOriginReadOnly(t *testing.T) {
	for _, origin := range []string{"AUTO", "MANUAL"} {
		base := "ATM_TEST_PI_" + origin
		id, native, handle := os.Getenv(base+"_ORCA_ID"), os.Getenv(base+"_NATIVE_ID"), os.Getenv(base+"_HANDLE")
		if id == "" || native == "" || handle == "" {
			t.Skip("requires separately supplied Pi test fixture identifiers")
		}
		title, source, bound := ResolveTitleOrigin(context.Background(), "pi", native, handle, id)
		if !bound || title != "Pi Chat" || (source == "auto") != (origin == "AUTO") || (source == "manual") != (origin == "MANUAL") {
			t.Fatalf("could not ascertain the supplied %s Pi binding: bound=%v, origin=%s", origin, bound, source)
		}
	}
}
