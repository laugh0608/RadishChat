package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type replyWireMessage struct {
	ID       int64          `json:"id"`
	Author   map[string]any `json:"author"`
	Markdown string         `json:"markdown"`
	SentAt   int64          `json:"sentAt"`
}

func postBotReply(t *testing.T, client *http.Client, origin, token string, roomID int64, event, markdown string,
	status int, out *replyWireMessage, installationHeaders ...string,
) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"markdown": markdown})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/api/v1/bot/rooms/%d/events/%s/reply", origin, roomID, event)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, install := range installationHeaders {
		req.Header.Add(botInstallationHeader, install)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reply status = %d, want %d and no-store", res.StatusCode, status)
	}
	if status == http.StatusUnauthorized && !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer ") {
		t.Fatal("reply response omitted Bearer challenge")
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

func replyHistory(t *testing.T, client *http.Client, origin, path string, status int) []replyWireMessage {
	t.Helper()
	var history struct {
		Messages []replyWireMessage `json:"messages"`
	}
	call(t, client, origin, http.MethodGet, path, nil, status, &history)
	return history.Messages
}

func TestBotReplyDeduplicatesAndUsesAuthorizedMessageHistory(t *testing.T) {
	f := newBotCommandFixture(t)
	origin := f.host.URL
	client := browser(t)
	permissions := (botCommandPermissions{receive: true, reply: true}).list()
	install := f.installWithPermissions(t, f.roomID, permissions, true).InstallationID
	historyPath := fmt.Sprintf("/rooms/%d/messages", f.roomID)
	var human message
	call(t, f.member, origin, http.MethodPost, historyPath,
		map[string]string{"markdown": "human message", "requestId": "fixture-human-request"}, http.StatusCreated, &human)

	event := invoke(t, f.member, origin, commandPath(f.roomID, install), "ping", "fixture-bot-reply-once", http.StatusAccepted)
	stream, stop := startStream(t, origin, f.member)
	defer stop()
	readSync(t, stream)
	postBotReply(t, client, origin, f.token, f.roomID, event, "  **pong**\n", http.StatusCreated, nil, install)
	readSync(t, stream)

	var retried replyWireMessage
	postBotReply(t, client, origin, f.token, f.roomID, event, "  **pong**\n", http.StatusOK, &retried, install)
	postBotReply(t, client, origin, f.token, f.roomID, event, "different", http.StatusConflict, nil, install)
	entries := replyHistory(t, f.member, origin, historyPath, http.StatusOK)
	if len(entries) != 2 || !reflect.DeepEqual(entries[1], retried) || retried.ID <= human.ID ||
		retried.SentAt <= 0 || retried.Markdown != "  **pong**\n" {
		t.Fatalf("unexpected message order, content or duplicate: %+v", entries)
	}
	humanAuthor := map[string]any{"id": float64(f.memberInfo.ID), "username": f.memberInfo.Username, "displayName": f.memberInfo.DisplayName}
	botAuthor := map[string]any{"kind": "bot", "id": f.record.ID, "displayName": f.record.DisplayName}
	if !reflect.DeepEqual(entries[0].Author, humanAuthor) || !reflect.DeepEqual(retried.Author, botAuthor) {
		t.Fatalf("unexpected author shapes: %+v", entries)
	}
	after := replyHistory(t, f.groupOwner, origin, fmt.Sprintf("%s?after=%d", historyPath, human.ID), http.StatusOK)
	if len(after) != 1 || !reflect.DeepEqual(after[0], retried) {
		t.Fatalf("Bot reply not found in forward pagination: %+v", after)
	}
	before := replyHistory(t, f.groupOwner, origin, fmt.Sprintf("%s?before=%d", historyPath, retried.ID), http.StatusOK)
	if len(before) != 1 || !reflect.DeepEqual(before[0], entries[0]) {
		t.Fatalf("human message not found in backward pagination: %+v", before)
	}
	late, lateUser := addUser(t, origin, "later-member")
	call(t, f.groupOwner, origin, http.MethodPost, fmt.Sprintf("/rooms/%d/members", f.roomID),
		map[string]string{"username": lateUser.Username}, http.StatusNoContent)
	if history := replyHistory(t, late, origin, historyPath+"?after=0", http.StatusOK); len(history) != 0 {
		t.Fatal("late member could see history from before joining")
	}
	replyHistory(t, f.botOwner, origin, historyPath, http.StatusNotFound)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, err := http.NewRequestWithContext(t.Context(), method, origin+"/api/v1"+historyPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := http.StatusUnauthorized
		if method == http.MethodPost {
			want = http.StatusForbidden
		}
		if res.StatusCode != want {
			t.Fatalf("Bot credential reached human message route: got %d, want %d", res.StatusCode, want)
		}
	}

	database := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := CopyDatabase(t.Context(), f.cfg.Database, database); err != nil {
		t.Fatal(err)
	}
	restored := newInstance(t, Config{Database: database})
	restartedMember := browser(t)
	call(t, restartedMember, restored.host.URL, http.MethodPost, "/login",
		credentials{Username: f.memberInfo.Username, Password: "fixture-only-password"}, http.StatusOK)
	if got := replyHistory(t, restartedMember, restored.host.URL, historyPath, http.StatusOK); !reflect.DeepEqual(got, entries) {
		t.Fatalf("restore changed message history: %+v", got)
	}
	checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_installations", 0)
	checkDBInt(t, restored.s.db, "SELECT count(*) FROM bot_events", 0)
	postBotReply(t, client, restored.host.URL, f.token, f.roomID, event, "  **pong**\n", http.StatusUnauthorized, nil, install)
}

func TestBotReplyRequiresLiveEventAndCurrentGrant(t *testing.T) {
	f := newBotCommandFixture(t)
	origin, client := f.host.URL, browser(t)
	permissions := (botCommandPermissions{receive: true, reply: true}).list()
	for _, invalid := range [][]string{
		{botInstallationRead, botCommandsReply},
		{botCommandsReceive, botCommandsReply},
		{botInstallationRead, botCommandsReceive, botCommandsReply, botCommandsReply},
	} {
		call(t, f.groupOwner, origin, http.MethodPut, f.installationPath(f.roomID),
			map[string][]string{"permissions": invalid}, http.StatusBadRequest)
	}
	install := f.installWithPermissions(t, f.roomID, permissions, false).InstallationID
	postBotReply(t, client, origin, f.token, f.roomID, botEventCursor(install, 1), "pong", http.StatusNotFound, nil, install)
	f.approve(t, install)
	path := commandPath(f.roomID, install)
	event := invoke(t, f.member, origin, path, "ping", "fixture-bot-reply-expiry", http.StatusAccepted)
	postBotReply(t, f.member, origin, "", f.roomID, event, "pong", http.StatusUnauthorized, nil, install)
	postBotReply(t, client, origin, f.token, f.roomID, event, "pong", http.StatusNotFound, nil)
	postBotReply(t, client, origin, f.token, f.roomID, event, "pong", http.StatusNotFound, nil, install, install)
	postBotReply(t, client, origin, f.token, f.roomID, botEventCursor(install, 99), "pong", http.StatusNotFound, nil, install)
	postBotReply(t, client, origin, f.token, f.roomID, event, " \n", http.StatusBadRequest, nil, install)
	postBotReply(t, client, origin, f.token, f.roomID, event, strings.Repeat("萝", 8001), http.StatusBadRequest, nil, install)

	otherRoom := createTestGroup(t, f.groupOwner, origin, "Only receives")
	other := f.installation(t, otherRoom, true, true).InstallationID
	otherEvent := invoke(t, f.groupOwner, origin, commandPath(otherRoom, other), "ping", "fixture-receive-only-reply", http.StatusAccepted)
	postBotReply(t, client, origin, f.token, otherRoom, otherEvent, "pong", http.StatusNotFound, nil, other)
	postBotReply(t, client, origin, f.token, f.roomID, otherEvent, "pong", http.StatusNotFound, nil, install)

	if _, err := f.s.db.Exec("UPDATE bot_events SET occurred_at=? WHERE installation_id=?",
		time.Now().Add(-botEventRetention-time.Minute).UnixMilli(), install); err != nil {
		t.Fatal(err)
	}
	postBotReply(t, client, origin, f.token, f.roomID, event, "pong", http.StatusGone, nil, install)
	fresh := invoke(t, f.member, origin, path, "ping", "fixture-bot-reply-revoke", http.StatusAccepted)
	rotated := rotateTestBot(t, f.botOwner, origin, f.record.ID)
	postBotReply(t, client, origin, f.token, f.roomID, fresh, "pong", http.StatusUnauthorized, nil, install)
	call(t, f.groupOwner, origin, http.MethodDelete, "/bot-installations/"+install, nil, http.StatusNoContent)
	freshInstall := f.installWithPermissions(t, f.roomID, permissions, true).InstallationID
	postBotReply(t, client, origin, rotated, f.roomID, fresh, "pong", http.StatusNotFound, nil, install)
	postBotReply(t, client, origin, rotated, f.roomID, fresh, "pong", http.StatusNotFound, nil, freshInstall)
	checkDBInt(t, f.s.db, "SELECT count(*) FROM messages", 0)
}

func TestMigrateV6KeepsMessagesAndGrantsAndRollsBackOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	legacy, err := sqliteDatabase(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schemaV1 + migrateV1ToV2 + migrateV2ToV3 + migrateV3ToV4 + migrateV4ToV5 + migrateV5ToV6 + `
INSERT INTO users(id,username,display_name) VALUES(1,'alice','Alice');
INSERT INTO rooms(id,kind,name,owner_id) VALUES(1,'group','Fixture',1);
INSERT INTO members(room_id,user_id,membership_id,joined_after_message_id) VALUES(1,1,'fixture-membership',7);
INSERT INTO messages(id,room_id,sender_id,body,sent_at,request_id) VALUES(9,1,1,'legacy markdown',42,'legacy-message');
INSERT INTO bots(id,owner_id,display_name) VALUES('fixture-bot',1,'Fixture Bot');
INSERT INTO bot_installations(id,room_id,bot_id,requested_by,state,permission,commands_receive,last_event_sequence)
  VALUES('fixture-installation',1,'fixture-bot',1,'active','installation:read',1,1);
INSERT INTO bot_events(installation_id,sequence,occurred_at,kind,version,command)
  VALUES('fixture-installation',1,123,'command.requested',1,'ping');
DROP INDEX messages_room;
CREATE INDEX messages_room ON rooms(name);
`); err != nil {
		t.Fatal(err)
	}
	if next, err := openDB(path); err == nil {
		next.Close()
		t.Fatal("v7 migration unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "SQLite 从 v6 迁移到 v7 失败") {
		t.Fatal(err)
	}
	checkDBInt(t, legacy, "PRAGMA user_version", 6)
	checkDBInt(t, legacy, "SELECT count(*) FROM messages WHERE id=9 AND sender_id=1", 1)
	checkDBInt(t, legacy, "SELECT count(*) FROM pragma_table_info('bot_installations') WHERE name='commands_reply'", 0)
	checkDBInt(t, legacy, "SELECT count(*) FROM sqlite_schema WHERE name='messages_v6'", 0)
	if _, err := legacy.Exec("DROP INDEX messages_room; CREATE INDEX messages_room ON messages(room_id,id DESC)"); err != nil {
		t.Fatal(err)
	}
	migrated, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	checkDBInt(t, migrated, "PRAGMA user_version", currentSchemaVersion)
	checkDBInt(t, migrated, "SELECT count(*) FROM messages WHERE id=9 AND room_id=1 AND sender_id=1 AND sender_bot_id IS NULL AND body='legacy markdown' AND sent_at=42 AND request_id='legacy-message'", 1)
	checkDBInt(t, migrated, "SELECT count(*) FROM members WHERE joined_after_message_id=7 AND membership_id='fixture-membership'", 1)
	checkDBInt(t, migrated, "SELECT count(*) FROM bot_installations WHERE commands_receive=1 AND commands_reply=0 AND last_event_sequence=1", 1)
	checkDBInt(t, migrated, "SELECT count(*) FROM bot_events WHERE sequence=1 AND command='ping'", 1)
	checkDBInt(t, migrated, "SELECT count(*) FROM pragma_foreign_key_check", 0)
}
