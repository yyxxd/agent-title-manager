// Command seed-data creates a real engine-compatible SQLite database in an
// isolated data dir and inserts a marker session for update acceptance.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/yyxxd/agent-title-manager/internal/engine"
	_ "modernc.org/sqlite"
)

func main() {
	dataDir := flag.String("data", "", "isolated data dir")
	flag.Parse()
	if *dataDir == "" {
		fmt.Fprintln(os.Stderr, "data required")
		os.Exit(2)
	}
	core, err := engine.Open(*dataDir, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := core.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	db, err := sql.Open("sqlite", "file:"+*dataDir+"/engine.sqlite3")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR REPLACE INTO sessions(agent,session_id,revision,title,manual)
		VALUES('codex','acceptance-session','r1','验收保留标题',0)`); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("seeded real engine schema")
}
