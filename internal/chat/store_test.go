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
	checkDBInt(t, db, "PRAGMA user_version", currentSchemaVersion)
	checkDBInt(t, db, "SELECT count(*) FROM pragma_table_info('members') WHERE name='joined_after_message_id'", 1)
	checkDBInt(t, db, "SELECT count(*) FROM pragma_table_info('members') WHERE name='membership_id'", 1)
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("unknown version opened successfully")
	} else if !strings.Contains(err.Error(), "未支持的数据库版本 999") {
		t.Fatal(err)
	}
	checkDBInt(t, db, "PRAGMA user_version", 999)
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
		checkDBInt(t, db, "PRAGMA user_version", currentSchemaVersion)
		checkDBInt(t, db, "SELECT count(*) FROM members WHERE room_id=1 AND joined_after_message_id=7", 2)
		checkDBInt(t, db, "SELECT count(*) FROM members WHERE room_id=2 AND joined_after_message_id=0", 2)
		checkDBInt(t, db, "SELECT count(*) FROM messages", 2)
		checkDBInt(t, db, "SELECT count(DISTINCT membership_id) FROM members WHERE length(membership_id)=32", 4)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateV2PreservesBoundaryAndMembershipSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + migrateV1ToV2 + `
INSERT INTO users(id,username,display_name) VALUES (1,'alice','Alice');
INSERT INTO rooms(id,kind,name,owner_id) VALUES (2,'group','Fixture',1);
INSERT INTO members(room_id,user_id,joined_after_message_id) VALUES (2,1,7);
INSERT INTO messages(id,room_id,sender_id,body,sent_at,request_id)
  VALUES (8,2,1,'Newer than the current boundary',0,'fixture_group_msg');
CREATE TRIGGER fixture_stop_v3 BEFORE UPDATE ON members
  BEGIN SELECT RAISE(ABORT,'fixture blocked membership IDs'); END;
`); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("v3 migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "fixture blocked membership IDs") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", 2)
	checkDBInt(t, legacy, "SELECT count(*) FROM pragma_table_info('members') WHERE name='membership_id'", 0)
	checkDBInt(t, legacy, "SELECT joined_after_message_id FROM members", 7)
	if _, err := legacy.Exec("DROP TRIGGER fixture_stop_v3"); err != nil {
		t.Fatal(err)
	}
	var firstID string
	for range 2 {
		db, err := openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		checkDBInt(t, db, "PRAGMA user_version", currentSchemaVersion)
		checkDBInt(t, db, "SELECT joined_after_message_id FROM members", 7)
		var id string
		if err := db.QueryRow("SELECT membership_id FROM members").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if len(id) != 32 || (firstID != "" && firstID != id) {
			t.Fatal("membership ID is missing or changed after reopening the migrated database")
		}
		firstID = id
	}
}

func TestMigrateV3RollsBackBotSchemaWhenIndexCreationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sqliteDatabase(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + migrateV1ToV2 + migrateV2ToV3 + `
INSERT INTO users(id,username,display_name) VALUES (1,'alice','Alice');
CREATE INDEX bots_owner ON users(display_name);
`); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("v4 migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "SQLite 从 v3 迁移到 v4 失败") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", 3)
	checkDBInt(t, legacy, "SELECT count(*) FROM sqlite_schema WHERE name='bots'", 0)
	checkDBInt(t, legacy, "SELECT count(*) FROM users WHERE username='alice'", 1)
	if _, err := legacy.Exec("DROP INDEX bots_owner"); err != nil {
		t.Fatal(err)
	}
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkDBInt(t, db, "PRAGMA user_version", currentSchemaVersion)
	checkDBInt(t, db, "SELECT count(*) FROM bots", 0)
	checkDBInt(t, db, "SELECT count(*) FROM users WHERE username='alice'", 1)
}

func TestMigrateV4PreservesBotCredentialAndRollsBackInstallationDDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sqliteDatabase(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + migrateV1ToV2 + migrateV2ToV3 + migrateV3ToV4 + `
INSERT INTO users(id,username,display_name) VALUES (1,'alice','Alice');
INSERT INTO rooms(id,kind,name,owner_id) VALUES (1,'group','Fixture',1);
INSERT INTO members(room_id,user_id,joined_after_message_id,membership_id) VALUES (1,1,7,'fixture-v4-membership');
INSERT INTO messages(id,room_id,sender_id,body,sent_at,request_id) VALUES (7,1,1,'Fixture',0,'fixture-v4-message');
CREATE INDEX bot_installations_bot ON bots(display_name);
`); err != nil {
		t.Fatal(err)
	}
	token := botTokenPrefix + randomText(botTokenBytes)
	if _, err := legacy.Exec("INSERT INTO bots(id,owner_id,display_name,token_hash) VALUES ('fixture',1,'Fixture Bot',?)", digest(token)); err != nil {
		t.Fatal(err)
	}

	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("v5 migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "SQLite 从 v4 迁移到 v5 失败") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", 4)
	checkDBInt(t, legacy, "SELECT count(*) FROM sqlite_schema WHERE name='bot_installations'", 0)
	checkDBInt(t, legacy, "SELECT count(*) FROM bots WHERE token_hash IS NOT NULL", 1)
	if _, err := legacy.Exec("DROP INDEX bot_installations_bot"); err != nil {
		t.Fatal(err)
	}

	opened := newInstance(t, Config{Database: path})
	checkDBInt(t, opened.s.db, "PRAGMA user_version", currentSchemaVersion)
	checkDBInt(t, opened.s.db, "SELECT count(*) FROM bot_installations", 0)
	checkDBInt(t, opened.s.db, "SELECT joined_after_message_id FROM members WHERE membership_id='fixture-v4-membership'", 7)
	checkDBInt(t, opened.s.db, "SELECT count(*) FROM messages WHERE id=7", 1)
	checkBotToken(t, opened.host.URL, token, 200, nil)
	testBotRoomAccess(t, opened.host.URL, token, 1, "", 404)
}
