package engine

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestRecordVisitReadsAllExistingRemindersAndPreservesHistory(t *testing.T) {
	dir := t.TempDir()
	// 从已发版的 v10 数据升级，同一毫秒内也可能出现多个提醒。
	db, err := sql.Open("sqlite", filepath.Join(dir, "engine.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{schema, schemaV2, schemaV3, schemaV4, schemaV5,
		schemaV6, schemaV7, schemaV8, schemaV9, schemaV10,
		`INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id) VALUES('codex','s','r','title',0,'orca')`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 61 {
		id := fmt.Sprint(i)
		_, err := db.Exec(`INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_base,orca_origin,sync_status,created,updated)
			VALUES(?,'codex','s',?,'title','title',CASE WHEN ?=60 THEN 'native' ELSE 'failed' END,'orca','','manual',
				CASE WHEN ?=60 THEN 'protected' WHEN ?=0 THEN 'failed' ELSE '' END,1,1)`, id, id, i, i, i)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started,input_tokens,output_tokens)
		VALUES('0','codex','api','fixture','openai_compatible','default','completed',1,18,2)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	e := engineAt(t, dir, nil)
	check := func(method string, p listRequest, want int) recordList {
		t.Helper()
		result := call[recordList](t, e, method, p)
		if result.Counts.Unread != want {
			t.Fatalf("%s: want %d unread, got %+v", method, want, result.Counts)
		}
		return result
	}
	check("records.list", listRequest{}, 61)
	// 翻页、搜索和后台预读不会消费提醒。进页即使只看到一条也会清除全库提醒。
	check("records.list", listRequest{Filter: "attention", Limit: 1}, 61)
	visit := check("records.open", listRequest{Filter: "attention", Limit: 1}, 0)
	if visit.Counts.Total != 61 || visit.Counts.Attention != 61 || len(visit.Items) != 1 || !visit.Items[0].CanAdopt {
		t.Fatalf("lost history or protected action: %+v", visit)
	}
	check("records.open", listRequest{}, 0)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = engineAt(t, dir, nil)
	list := check("records.list", listRequest{Limit: 100}, 0)
	last := list.Items[len(list.Items)-1]
	if last.ID != "0" || last.Status != "failed" || last.SyncStatus != "failed" ||
		len(last.Attempts) != 1 || last.Attempts[0].InputTokens == nil || *last.Attempts[0].InputTokens != 18 ||
		last.Attempts[0].OutputTokens == nil || *last.Attempts[0].OutputTokens != 2 {
		t.Fatalf("reading changed the failure or usage: %+v", last)
	}
	if _, err := e.db.Exec(`UPDATE events SET sync_status='writing' WHERE id='0';
		UPDATE events SET sync_status='failed' WHERE id='0';
		INSERT INTO events(id,agent,session_id,revision,base,title,status,orca_id,orca_origin,created,updated)
		VALUES('new','codex','s','new','title','title','failed','','',1,1)`); err != nil {
		t.Fatal(err)
	}
	check("records.list", listRequest{}, 2)
	check("records.open", listRequest{Query: "no match"}, 0)
	check("records.list", listRequest{}, 0)
}
