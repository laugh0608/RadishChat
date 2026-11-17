package chat

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseBackupAndRestoreInvalidateAuthentication(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	original := newInstance(t, Config{Database: source, Registration: RegistrationOpen})
	origin := original.host.URL
	alice, _ := addUser(t, origin, "alice")
	bob, _ := addUser(t, origin, "bob")
	botRecord := createTestBot(t, bob, origin, "Fixture Bot")
	botToken := rotateTestBot(t, bob, origin, botRecord.ID)
	var group room
	call(t, alice, origin, "POST", "/rooms/group", map[string]any{"name": "Fixture", "usernames": []string{"bob"}}, 201, &group)
	path := fmt.Sprintf("/rooms/%d", group.ID)
	active := installTestBot(t, alice, origin, path+"/bots/"+botRecord.ID+"/installation")
	approvePath := "/bots/" + botRecord.ID + "/installations/"
	call(t, bob, origin, http.MethodPost, approvePath+active.InstallationID+"/approve", nil, http.StatusNoContent)
	pendingGroup := createTestGroup(t, alice, origin, "Pending group")
	pending := installTestBot(t, alice, origin, fmt.Sprintf("/rooms/%d/bots/%s/installation", pendingGroup, botRecord.ID))
	body := map[string]string{"markdown": "Restore this message", "requestId": "restore-fixture-request"}
	call(t, alice, origin, "POST", path+"/messages", body, 201)
	call(t, bob, origin, "GET", "/me", nil, 200)
	serviceURL, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	cookies := bob.Jar.Cookies(serviceURL)
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	if _, err := original.s.db.Exec("INSERT INTO oidc_flows VALUES ('state', x'01', 'nonce', 'verifier', 9999999999)"); err != nil {
		t.Fatal(err)
	}
	if _, err := original.s.db.Exec(`INSERT INTO users(id,username,display_name,password,status)
		VALUES (3,'locked','Locked','fixture-hash','recovery_required'),(4,'suspended','Suspended','fixture-hash','disabled');
		INSERT INTO account_recovery_codes VALUES (3,randomblob(32),9999999999);`); err != nil {
		t.Fatal(err)
	}
	original.host.Close()
	if err := original.s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newInstance(t, Config{Database: source})
	checkBotToken(t, restarted.host.URL, botToken, http.StatusOK, nil)
	testBotRoomAccess(t, restarted.host.URL, botToken, group.ID, active.InstallationID, http.StatusOK)
	testBotRoomAccess(t, restarted.host.URL, botToken, pendingGroup, pending.InstallationID, http.StatusNotFound)
	restarted.host.Close()
	if err := restarted.s.Close(); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(dir, "private", "backup.sqlite")
	if err := CopyDatabase(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{source, backup} {
		db, err := sqliteDatabase(input, "ro")
		if err != nil {
			t.Fatal(err)
		}
		checkDBInt(t, db, "PRAGMA user_version", currentSchemaVersion)
		checkDBInt(t, db, "SELECT count(*) FROM members", 3)
		checkDBInt(t, db, "SELECT count(*) FROM bots", 1)
		if input == source {
			checkDBInt(t, db, "SELECT count(*) FROM account_recovery_codes", 1)
			checkDBInt(t, db, "SELECT count(*) FROM sessions", 2)
			checkDBInt(t, db, "SELECT count(*) FROM oidc_flows", 1)
			checkDBInt(t, db, "SELECT count(*) FROM bots WHERE token_hash IS NOT NULL", 1)
			checkDBInt(t, db, "SELECT count(*) FROM bot_installations", 2)
		} else {
			checkDBInt(t, db, "SELECT count(*) FROM account_recovery_codes", 0)
			checkDBInt(t, db, "SELECT count(*) FROM sessions", 0)
			checkDBInt(t, db, "SELECT count(*) FROM oidc_flows", 0)
			checkDBInt(t, db, "SELECT count(*) FROM bots WHERE token_hash IS NOT NULL", 0)
			checkDBInt(t, db, "SELECT count(*) FROM bot_installations", 0)
		}
		db.Close()

		t.Run(filepath.Base(input), func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "new", "chat.sqlite")
			if err := CopyDatabase(t.Context(), input, target); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("unexpected database permissions: %v, %v", info, err)
			}
			restored := newInstance(t, Config{Database: target})
			restoredURL, err := url.Parse(restored.host.URL)
			if err != nil {
				t.Fatal(err)
			}
			c := browser(t)
			c.Jar.SetCookies(restoredURL, cookies)
			checkDBInt(t, restored.s.db, "SELECT count(*) FROM oidc_flows", 0)
			checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_installations", 0)
			checkDBInt(t, restored.s.db, "SELECT count(*) FROM account_recovery_codes", 0)
			checkDBInt(t, restored.s.db, "SELECT count(*) FROM users WHERE id=3 AND status='recovery_required'", 1)
			checkDBInt(t, restored.s.db, "SELECT count(*) FROM users WHERE id=4 AND status='disabled'", 1)
			call(t, c, restored.host.URL, "GET", "/me", nil, http.StatusUnauthorized)
			checkBotToken(t, restored.host.URL, botToken, http.StatusUnauthorized, nil)
			call(t, c, restored.host.URL, "POST", "/login", credentials{Username: "bob", Password: "fixture-only-password"}, 200)
			var bots []bot
			call(t, c, restored.host.URL, "GET", "/bots", nil, http.StatusOK, &bots)
			if len(bots) != 1 || bots[0] != botRecord {
				t.Fatal("restored Bot identity or ownership changed")
			}
			newToken := rotateTestBot(t, c, restored.host.URL, botRecord.ID)
			checkBotToken(t, restored.host.URL, newToken, http.StatusOK, nil)
			checkBotToken(t, restored.host.URL, botToken, http.StatusUnauthorized, nil)
			testBotRoomAccess(t, restored.host.URL, newToken, group.ID, active.InstallationID, http.StatusNotFound)
			call(t, c, restored.host.URL, http.MethodPost, approvePath+pending.InstallationID+"/approve", nil, http.StatusNotFound)
			var members []user
			call(t, c, restored.host.URL, "GET", path+"/members", nil, 200, &members)
			var page messagePage
			call(t, c, restored.host.URL, "GET", path+"/messages", nil, 200, &page)
			if len(members) != 2 || len(page.Messages) != 1 || page.Messages[0].Markdown != body["markdown"] {
				t.Fatal("restored group data does not match")
			}
		})
	}
}

func TestCopyDatabaseKeepsOlderSQLiteVersions(t *testing.T) {
	for index, schema := range []string{
		schemaV1,
		schemaV1 + migrateV1ToV2,
		schemaV1 + migrateV1ToV2 + migrateV2ToV3,
		schemaV1 + migrateV1ToV2 + migrateV2ToV3 + migrateV3ToV4,
		schemaV1 + migrateV1ToV2 + migrateV2ToV3 + migrateV3ToV4 + migrateV4ToV5,
	} {
		version := index + 1
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.sqlite")
			legacy, err := sqliteDatabase(source, "rwc")
			if err != nil {
				t.Fatal(err)
			}
			defer legacy.Close()
			if _, err := legacy.Exec(schema + `INSERT INTO oidc_flows VALUES ('state',x'01','nonce','verifier',9999999999);`); err != nil {
				t.Fatal(err)
			}
			if version == botSchemaVersion {
				if _, err := legacy.Exec(`INSERT INTO users(id,username,display_name) VALUES (1,'alice','Alice');
INSERT INTO bots(id,owner_id,display_name,token_hash) VALUES ('fixture',1,'Fixture Bot',randomblob(32));`); err != nil {
					t.Fatal(err)
				}
			}
			copyPath := filepath.Join(dir, "copy.sqlite")
			if err := CopyDatabase(t.Context(), source, copyPath); err != nil {
				t.Fatal(err)
			}
			copyDB, err := sqliteDatabase(copyPath, "ro")
			if err != nil {
				t.Fatal(err)
			}
			defer copyDB.Close()
			checkDBInt(t, copyDB, "PRAGMA user_version", version)
			checkDBInt(t, copyDB, "SELECT count(*) FROM oidc_flows", 0)
			checkDBInt(t, copyDB, "SELECT count(*) FROM sqlite_schema WHERE name='bot_events'", 0)
			if version < botInstallationSchemaVersion {
				checkDBInt(t, copyDB, "SELECT count(*) FROM sqlite_schema WHERE name='bot_installations'", 0)
			}
			if version == botSchemaVersion {
				checkDBInt(t, copyDB, "SELECT count(*) FROM bots WHERE token_hash IS NULL", 1)
				checkDBInt(t, legacy, "SELECT count(*) FROM bots WHERE token_hash IS NOT NULL", 1)
			}
			checkDBInt(t, legacy, "SELECT count(*) FROM oidc_flows", 1)
		})
	}
}

func TestCopyDatabaseRejectsInvalidInputWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.sqlite")
	if err := os.WriteFile(invalid, []byte("fixture is not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "restored.sqlite")
	for _, source := range []string{invalid, filepath.Join(dir, "missing.sqlite")} {
		if err := CopyDatabase(t.Context(), source, target); err == nil {
			t.Fatalf("copied invalid database %s", source)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial target exists: %v", err)
		}
	}
	for _, suffix := range []string{"", "-wal"} {
		blocker := target + suffix
		if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := CopyDatabase(t.Context(), invalid, target); !errors.Is(err, os.ErrExist) {
			t.Fatalf("expected existing target to be rejected: %v", err)
		}
		if data, err := os.ReadFile(blocker); err != nil || string(data) != "keep" {
			t.Fatalf("existing file changed: %v", err)
		}
		if err := os.Remove(blocker); err != nil {
			t.Fatal(err)
		}
	}
}
