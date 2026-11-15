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
	var group room
	call(t, alice, origin, "POST", "/rooms/group", map[string]any{"name": "Fixture", "usernames": []string{"bob"}}, 201, &group)
	path := fmt.Sprintf("/rooms/%d", group.ID)
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
	original.host.Close()
	if err := original.s.Close(); err != nil {
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
		checkDBInt(t, db, "SELECT count(*) FROM members", 2)
		if input == source {
			checkDBInt(t, db, "SELECT count(*) FROM sessions", 2)
			checkDBInt(t, db, "SELECT count(*) FROM oidc_flows", 1)
		} else {
			checkDBInt(t, db, "SELECT count(*) FROM sessions", 0)
			checkDBInt(t, db, "SELECT count(*) FROM oidc_flows", 0)
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
			call(t, c, restored.host.URL, "GET", "/me", nil, http.StatusUnauthorized)
			call(t, c, restored.host.URL, "POST", "/login", credentials{Username: "bob", Password: "fixture-only-password"}, 200)
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
