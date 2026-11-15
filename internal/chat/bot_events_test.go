package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type botCommandFixture struct {
	instance
	groupOwner *http.Client
	member     *http.Client
	botOwner   *http.Client
	memberInfo user
	record     bot
	token      string
	roomID     int64
}

func newBotCommandFixture(t *testing.T) botCommandFixture {
	t.Helper()
	i := newInstance(t, Config{Registration: RegistrationOpen})
	base := i.host.URL
	owner, _ := addUser(t, base, "group-owner")
	member, person := addUser(t, base, "member")
	developer, _ := addUser(t, base, "developer")
	record := createTestBot(t, developer, base, "Command Bot")
	return botCommandFixture{
		instance: i, groupOwner: owner, member: member, botOwner: developer, memberInfo: person, record: record,
		token:  rotateTestBot(t, developer, base, record.ID),
		roomID: createTestGroup(t, owner, base, "Command group", person.Username),
	}
}

func (f botCommandFixture) installationPath(roomID int64) string {
	return fmt.Sprintf("/rooms/%d/bots/%s/installation", roomID, f.record.ID)
}

func (f botCommandFixture) installation(t *testing.T, roomID int64, receives, approve bool) botInstallationPreview {
	t.Helper()
	return f.installWithPermissions(t, roomID, (botCommandPermissions{receive: receives}).list(), approve)
}

func (f botCommandFixture) installWithPermissions(t *testing.T, roomID int64, permissions []string, approve bool) botInstallationPreview {
	t.Helper()
	var view botInstallationPreview
	call(t, f.groupOwner, f.host.URL, http.MethodPut, f.installationPath(roomID),
		map[string][]string{"permissions": permissions}, http.StatusOK, &view)
	if view.InstallationID == "" || !slices.Equal(view.Permissions, permissions) {
		t.Fatal("unexpected installation grant")
	}
	if approve {
		f.approve(t, view.InstallationID)
	}
	return view
}

func (f botCommandFixture) approve(t *testing.T, install string) {
	t.Helper()
	call(t, f.botOwner, f.host.URL, http.MethodPost, "/bots/"+f.record.ID+"/installations/"+install+"/approve",
		nil, http.StatusNoContent)
}

func commandPath(roomID int64, installationID string) string {
	return fmt.Sprintf("/rooms/%d/bot-installations/%s/commands", roomID, installationID)
}

func invoke(t *testing.T, client *http.Client, origin, path, command, requestID string, status int) string {
	t.Helper()
	var accepted botCommandAccepted
	res := call(t, client, origin, http.MethodPost, path,
		map[string]string{"command": command, "requestId": requestID}, status, &accepted)
	if status == http.StatusAccepted && accepted.EventID == "" {
		t.Fatal("accepted command omitted its event ID")
	}
	if status == http.StatusTooManyRequests && res.Header.Get("Retry-After") != "60" {
		t.Fatal("command response omitted Retry-After")
	}
	return accepted.EventID
}

func poll(t *testing.T, origin, token string, roomID int64, install, query string, status int, out any) {
	t.Helper()
	path := fmt.Sprintf("%s/api/v1/bot/rooms/%d/events", origin, roomID)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, path+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if install != "" {
		req.Header.Set(botInstallationHeader, install)
	}
	res, err := browser(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("event poll returned %d, want %d", res.StatusCode, status)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("event poll may be cached")
	}
	if status == http.StatusTooManyRequests && res.Header.Get("Retry-After") != "60" {
		t.Fatal("poll response omitted Retry-After")
	}
	if status == http.StatusUnauthorized && !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer ") {
		t.Fatal("poll response omitted Bearer challenge")
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBotCommandDeliveryAndRevocation(t *testing.T) {
	f := newBotCommandFixture(t)
	origin := f.host.URL
	original := f.installation(t, f.roomID, false, true)
	firstPath := commandPath(f.roomID, original.InstallationID)
	invoke(t, f.member, origin, firstPath, "ping", "fixture-command-001", http.StatusNotFound)
	poll(t, origin, f.token, f.roomID, original.InstallationID, "", http.StatusNotFound, nil)
	call(t, f.groupOwner, origin, http.MethodPut, f.installationPath(f.roomID),
		map[string][]string{"permissions": {botInstallationRead, botCommandsReceive}}, http.StatusConflict)
	call(t, f.groupOwner, origin, http.MethodDelete, "/bot-installations/"+original.InstallationID, nil, http.StatusNoContent)
	install := f.installation(t, f.roomID, true, false)
	call(t, f.groupOwner, origin, http.MethodPut, f.installationPath(f.roomID),
		map[string][]string{"permissions": {botCommandsReceive, botInstallationRead}}, http.StatusOK)
	path := commandPath(f.roomID, install.InstallationID)
	invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusNotFound)
	poll(t, origin, f.token, f.roomID, install.InstallationID, "", http.StatusNotFound, nil)
	f.approve(t, install.InstallationID)

	invoke(t, f.botOwner, origin, path, "ping", "fixture-command-001", http.StatusNotFound)
	invoke(t, browser(t), origin, path, "ping", "fixture-command-001", http.StatusUnauthorized)
	poll(t, origin, f.token, f.roomID, "", "", http.StatusNotFound, nil)
	call(t, f.member, origin, http.MethodPost, fmt.Sprintf("/rooms/%d/messages", f.roomID),
		map[string]string{"markdown": "ping is ordinary chat here", "requestId": "fixture-message-001"}, http.StatusCreated)
	var page botEventPage
	poll(t, origin, f.token, f.roomID, install.InstallationID, "", http.StatusOK, &page)
	if page.Events == nil || len(page.Events) != 0 || page.NextCursor == "" || page.HasMore {
		t.Fatal("ordinary chat produced an event or the empty cursor was missing")
	}
	first := invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusAccepted)
	if retry := invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusAccepted); retry != first {
		t.Fatal("retry changed the event")
	}
	invoke(t, f.member, origin, path, "different", "fixture-command-001", http.StatusConflict)
	invoke(t, f.member, origin, path, "different", "fixture-command-002", http.StatusBadRequest)

	if _, err := f.s.db.Exec("CREATE TRIGGER fail_command BEFORE INSERT ON bot_command_requests BEGIN SELECT RAISE(ABORT,'fixture rollback'); END"); err != nil {
		t.Fatal(err)
	}
	invoke(t, f.member, origin, path, "ping", "fixture-command-002", http.StatusInternalServerError)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_events", 1)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_command_requests", 1)
	checkDBInt(t, f.s.db, "SELECT max(last_event_sequence) FROM bot_installations", 1)
	if _, err := f.s.db.Exec("DROP TRIGGER fail_command"); err != nil {
		t.Fatal(err)
	}
	expected := []string{first}
	for i := 2; i <= 27; i++ {
		expected = append(expected, invoke(t, f.member, origin, path, "ping", fmt.Sprintf("fixture-command-%03d", i), http.StatusAccepted))
	}
	poll(t, origin, f.token, f.roomID, install.InstallationID, "", http.StatusOK, &page)
	if len(page.Events) != botEventDefaultPage || !page.HasMore || page.Events[0].ID != first {
		t.Fatal("default page did not stop at the agreed limit")
	}
	cursor := page.NextCursor
	var next, replay botEventPage
	poll(t, origin, f.token, f.roomID, install.InstallationID, "?after="+cursor, http.StatusOK, &next)
	poll(t, origin, f.token, f.roomID, install.InstallationID, "?after="+cursor, http.StatusOK, &replay)
	if next.HasMore || len(next.Events) != 2 || !slices.Equal(next.Events, replay.Events) {
		t.Fatal("re-reading from the same cursor was not stable")
	}
	for index, event := range append(page.Events, next.Events...) {
		if event.ID != expected[index] || event.Type != "command.requested" || event.Version != 1 ||
			event.Payload.Command != "ping" || event.OccurredAt <= 0 {
			t.Fatal("unexpected event order or payload")
		}
	}
	poll(t, origin, f.token, f.roomID, install.InstallationID, "?after="+next.NextCursor, http.StatusOK, &page)
	if len(page.Events) != 0 || page.NextCursor != next.NextCursor || page.HasMore {
		t.Fatal("empty page moved the consumer cursor")
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=1&limit=2", "?after=", "?after=broken",
		"?after=" + botEventCursor(install.InstallationID, 28)} {
		poll(t, origin, f.token, f.roomID, install.InstallationID, query, http.StatusBadRequest, nil)
	}

	otherRoom := createTestGroup(t, f.groupOwner, origin, "Other group")
	other := f.installation(t, otherRoom, true, true)
	poll(t, origin, f.token, otherRoom, install.InstallationID, "", http.StatusNotFound, nil)
	poll(t, origin, f.token, otherRoom, other.InstallationID, "?after="+cursor, http.StatusBadRequest, nil)
	call(t, f.groupOwner, origin, http.MethodDelete, fmt.Sprintf("/rooms/%d/members/%d", f.roomID, f.memberInfo.ID), nil, http.StatusNoContent)
	invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusNotFound)
	call(t, f.groupOwner, origin, http.MethodPost, fmt.Sprintf("/rooms/%d/members", f.roomID),
		map[string]string{"username": f.memberInfo.Username}, http.StatusNoContent)
	if recent := invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusAccepted); recent == first {
		t.Fatal("command dedupe reused the previous membership")
	}

	token := rotateTestBot(t, f.botOwner, origin, f.record.ID)
	poll(t, origin, f.token, f.roomID, install.InstallationID, "", http.StatusUnauthorized, nil)
	poll(t, origin, token, f.roomID, install.InstallationID, "?after="+next.NextCursor, http.StatusOK, &page)
	if len(page.Events) != 1 {
		t.Fatal("rotation did not retain the current grant and cursor")
	}
	call(t, f.groupOwner, origin, http.MethodDelete, "/bot-installations/"+install.InstallationID, nil, http.StatusNoContent)
	poll(t, origin, token, f.roomID, install.InstallationID, "", http.StatusNotFound, nil)
	invoke(t, f.member, origin, path, "ping", "fixture-command-001", http.StatusNotFound)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_events", 0)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_command_requests", 0)
	replacement := f.installation(t, f.roomID, true, true)
	poll(t, origin, token, f.roomID, replacement.InstallationID, "?after="+next.NextCursor, http.StatusBadRequest, nil)
	poll(t, origin, token, otherRoom, other.InstallationID, "", http.StatusOK, nil)
}

func TestBotEventCapacityExpiryAndCleanup(t *testing.T) {
	f := newBotCommandFixture(t)
	origin := f.host.URL
	install := f.installation(t, f.roomID, true, true).InstallationID
	path := commandPath(f.roomID, install)
	first := invoke(t, f.member, origin, path, "ping", "fixture-command-first", http.StatusAccepted)
	if _, err := f.s.db.Exec(`WITH RECURSIVE seq(n) AS (
		VALUES (2) UNION ALL SELECT n+1 FROM seq WHERE n<?
	) INSERT INTO bot_events(installation_id,sequence,occurred_at,kind,version,command)
	  SELECT ?,n,?,'command.requested',1,'ping' FROM seq`, botEventCapacity, install, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec("UPDATE bot_installations SET last_event_sequence=? WHERE id=?", botEventCapacity, install); err != nil {
		t.Fatal(err)
	}
	last := invoke(t, f.member, origin, path, "ping", "fixture-command-after-cap", http.StatusAccepted)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_events", botEventCapacity)
	if last != botEventCursor(install, botEventCapacity+1) ||
		invoke(t, f.member, origin, path, "ping", "fixture-command-first", http.StatusAccepted) != first {
		t.Fatal("capacity purge reset the sequence or removed the valid dedupe receipt")
	}
	var gap map[string]string
	poll(t, origin, f.token, f.roomID, install, "", http.StatusGone, &gap)
	if gap["resumeAfter"] != first {
		t.Fatal("capacity gap omitted the resume boundary")
	}
	expiredTime := time.Now().Add(-botEventRetention - time.Hour).UnixMilli()
	if _, err := f.s.db.Exec("UPDATE bot_events SET occurred_at=? WHERE installation_id=? AND sequence=2", expiredTime, install); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec("UPDATE bot_command_requests SET accepted_at=? WHERE installation_id=? AND event_sequence=1", expiredTime, install); err != nil {
		t.Fatal(err)
	}
	poll(t, origin, f.token, f.roomID, install, "?after="+first, http.StatusGone, &gap)
	if gap["resumeAfter"] != botEventCursor(install, 2) {
		t.Fatal("an expired event was visible before the cleanup ran")
	}
	if err := pruneExpiredBotEvents(t.Context(), f.s.db, time.Now()); err != nil {
		t.Fatal(err)
	}
	checkDBInt(t, f.s.db, "SELECT pruned_event_sequence FROM bot_installations", 2)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_events", botEventCapacity-1)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM bot_command_requests", 1)
	var page botEventPage
	poll(t, origin, f.token, f.roomID, install, "?limit=1&after="+gap["resumeAfter"], http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].ID != botEventCursor(install, 3) || !page.HasMore {
		t.Fatal("resuming after an expired prefix did not return the next event")
	}
	if retry := invoke(t, f.member, origin, path, "ping", "fixture-command-first", http.StatusAccepted); retry != botEventCursor(install, botEventCapacity+2) {
		t.Fatal("an expired receipt was not released")
	}
}

func TestBotDeliveryRateLimitsAreScoped(t *testing.T) {
	f := newBotCommandFixture(t)
	origin := f.host.URL
	install := f.installation(t, f.roomID, true, true).InstallationID
	path := commandPath(f.roomID, install)
	for n := range botCommandSubmitLimit {
		invoke(t, f.member, origin, path, "ping", fmt.Sprintf("fixture-command-%03d", n), http.StatusAccepted)
	}
	invoke(t, f.member, origin, path, "ping", "fixture-command-over-limit", http.StatusTooManyRequests)
	invoke(t, f.member, origin, path, "ping", "fixture-command-000", http.StatusAccepted)
	invoke(t, f.groupOwner, origin, path, "ping", "fixture-command-owner", http.StatusAccepted)

	for range botEventPollLimit {
		poll(t, origin, f.token, f.roomID, install, "?limit=1", http.StatusOK, nil)
	}
	poll(t, origin, f.token, f.roomID, install, "", http.StatusTooManyRequests, nil)
	otherRoom := createTestGroup(t, f.groupOwner, origin, "Other rate scope")
	other := f.installation(t, otherRoom, true, true).InstallationID
	poll(t, origin, f.token, otherRoom, other, "", http.StatusOK, nil)
}

func TestBotEventRestartAndRestore(t *testing.T) {
	f := newBotCommandFixture(t)
	install := f.installation(t, f.roomID, true, true).InstallationID
	event := invoke(t, f.member, f.host.URL, commandPath(f.roomID, install), "ping", "fixture-command-restart", http.StatusAccepted)
	f.host.Close()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newInstance(t, Config{Database: f.cfg.Database})
	var page botEventPage
	poll(t, restarted.host.URL, f.token, f.roomID, install, "", http.StatusOK, &page)
	if len(page.Events) != 1 || page.Events[0].ID != event {
		t.Fatal("normal restart did not retain the queue")
	}
	restarted.host.Close()
	if err := restarted.s.Close(); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(t.TempDir(), "backup.sqlite")
	if err := CopyDatabase(t.Context(), f.cfg.Database, backup); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{f.cfg.Database, backup} {
		target := filepath.Join(t.TempDir(), "restored.sqlite")
		if err := CopyDatabase(t.Context(), input, target); err != nil {
			t.Fatal(err)
		}
		restored := newInstance(t, Config{Database: target})
		checkDBInt(t, restored.s.db, "PRAGMA user_version", currentSchemaVersion)
		checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_events", 0)
		checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_command_requests", 0)
		checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_installations", 0)
		poll(t, restored.host.URL, f.token, f.roomID, install, "", http.StatusUnauthorized, nil)
		owner := browser(t)
		call(t, owner, restored.host.URL, http.MethodPost, "/login",
			credentials{Username: "developer", Password: "fixture-only-password"}, http.StatusOK)
		token := rotateTestBot(t, owner, restored.host.URL, f.record.ID)
		poll(t, restored.host.URL, token, f.roomID, install, "", http.StatusNotFound, nil)
	}
}

func TestMigrateV5KeepsLegacyBotPermissionsAndRollsBackOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sqliteDatabase(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + migrateV1ToV2 + migrateV2ToV3 + migrateV3ToV4 + migrateV4ToV5 + `
INSERT INTO users(id,username,display_name) VALUES(1,'alice','Alice');
INSERT INTO rooms(id,kind,name,owner_id) VALUES(1,'group','Fixture',1);
INSERT INTO members(room_id,user_id,membership_id) VALUES(1,1,'fixture-membership');
INSERT INTO bots(id,owner_id,display_name) VALUES('fixture-bot',1,'Fixture Bot');
INSERT INTO bot_installations(id,room_id,bot_id,requested_by,state,permission)
  VALUES('fixture-installation',1,'fixture-bot',1,'active','installation:read');
CREATE INDEX bot_events_expiry ON bots(display_name);
`); err != nil {
		t.Fatal(err)
	}
	token := botTokenPrefix + randomText(botTokenBytes)
	if _, err := legacy.Exec("UPDATE bots SET token_hash=? WHERE id='fixture-bot'", digest(token)); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("v6 migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "SQLite 从 v5 迁移到 v6 失败") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", botInstallationSchemaVersion)
	checkDBInt(t, legacy, "SELECT count(*) FROM bot_installations", 1)
	checkDBInt(t, legacy, "SELECT count(*) FROM pragma_table_info('bot_installations') WHERE name='commands_receive'", 0)
	checkDBInt(t, legacy, "SELECT count(*) FROM sqlite_schema WHERE name='bot_events'", 0)
	if _, err := legacy.Exec("DROP INDEX bot_events_expiry"); err != nil {
		t.Fatal(err)
	}
	migrated := newInstance(t, Config{Database: path})
	checkDBInt(t, migrated.s.db, "PRAGMA user_version", currentSchemaVersion)
	grant := testBotRoomAccess(t, migrated.host.URL, token, 1, "fixture-installation", http.StatusOK)
	if !slices.Equal(grant.Permissions, []string{botInstallationRead}) {
		t.Fatal("migration gave legacy installs command permissions")
	}
	poll(t, migrated.host.URL, token, 1, grant.InstallationID, "", http.StatusNotFound, nil)
}
