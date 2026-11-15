package chat

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func checkDBInt(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %d, want %d (error %v)", query, got, want, err)
	}
}

func TestOpenDBInitializesCurrentSchemaAndRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkDBInt(t, db, "PRAGMA user_version", 2)
	checkDBInt(t, db, "SELECT count(*) FROM pragma_table_info('members') WHERE name='joined_after_message_id'", 1)
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("unknown version opened successfully")
	} else if !strings.Contains(err.Error(), "未支持的数据库版本 3") {
		t.Fatal(err)
	}
	checkDBInt(t, db, "PRAGMA user_version", 3)
}

func TestMigrateV1RollsBackWhenBackfillFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + `
INSERT INTO users(id,username,display_name) VALUES (1,'alice','Alice'),(2,'bob','Bob');
INSERT INTO rooms(id,kind,name,owner_id) VALUES (1,'group','Fixture',1);
INSERT INTO rooms(id,kind,direct_a,direct_b) VALUES (2,'direct',1,2);
INSERT INTO members(room_id,user_id) VALUES (1,1),(1,2),(2,1),(2,2);
INSERT INTO messages(id,room_id,sender_id,body,sent_at,request_id)
  VALUES (7,1,1,'Fixture group',0,'fixture_group_msg'),(8,2,2,'Fixture direct',0,'fixture_direct_msg');
CREATE TRIGGER fixture_stop_backfill BEFORE UPDATE ON members
  BEGIN SELECT RAISE(ABORT,'fixture blocked membership backfill'); END;
`); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "fixture blocked membership backfill") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", 1)
	checkDBInt(t, legacy, "SELECT count(*) FROM pragma_table_info('members') WHERE name='joined_after_message_id'", 0)
	checkDBInt(t, legacy, "SELECT count(*) FROM members", 4)
	checkDBInt(t, legacy, "SELECT count(*) FROM messages", 2)
	if _, err := legacy.Exec("DROP TRIGGER fixture_stop_backfill"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		db, err := openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		checkDBInt(t, db, "PRAGMA user_version", 2)
		checkDBInt(t, db, "SELECT count(*) FROM members WHERE room_id=1 AND joined_after_message_id=7", 2)
		checkDBInt(t, db, "SELECT count(*) FROM members WHERE room_id=2 AND joined_after_message_id=0", 2)
		checkDBInt(t, db, "SELECT count(*) FROM messages", 2)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
