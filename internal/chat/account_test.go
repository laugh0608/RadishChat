package chat

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisableLocalAccountRevokesSessionsAndGrants(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin := i.host.URL
	target, account := addUser(t, origin, "target")
	other, peer := addUser(t, origin, "peer")
	second := browser(t)
	login := credentials{Username: account.Username, Password: "fixture-only-password"}
	call(t, second, origin, "POST", "/login", login, 200)
	room := createTestGroup(t, target, origin, "Target group", peer.Username)
	peerRoom := createTestGroup(t, other, origin, "Peer group")
	roomPath := fmt.Sprintf("/rooms/%d", room)
	historyPath := roomPath + "/messages"
	permissions := (botCommandPermissions{receive: true, reply: true}).list()
	install := func(client *http.Client, roomID int64, record bot) botInstallationPreview {
		var view botInstallationPreview
		call(t, client, origin, "PUT", fmt.Sprintf("/rooms/%d/bots/%s/installation", roomID, record.ID),
			map[string][]string{"permissions": permissions}, 200, &view)
		return view
	}
	approve := func(client *http.Client, record bot, view botInstallationPreview, status int) {
		call(t, client, origin, "POST", "/bots/"+record.ID+"/installations/"+view.InstallationID+"/approve", nil, status)
	}
	targetBot := createTestBot(t, target, origin, "Target Bot")
	targetToken := rotateTestBot(t, target, origin, targetBot.ID)
	targetGrant := install(target, room, targetBot)
	pendingOwned := install(other, peerRoom, targetBot)
	peerBot := createTestBot(t, other, origin, "Peer Bot")
	peerToken := rotateTestBot(t, other, origin, peerBot.ID)
	peerGrant := install(target, room, peerBot)
	approve(other, peerBot, peerGrant, 204)
	pendingBot := createTestBot(t, other, origin, "Pending Bot")
	pendingRequested := install(target, room, pendingBot)
	if targetGrant.State != "active" || pendingOwned.State != "pending" || pendingRequested.State != "pending" {
		t.Fatal("unexpected Bot grants in suspension fixture")
	}

	call(t, target, origin, "POST", historyPath, map[string]string{"markdown": "Previous message", "requestId": "before-disable-human"}, 201)
	event := invoke(t, other, origin, commandPath(room, targetGrant.InstallationID), "ping", "before-disable-bot-1", 202)
	postBotReply(t, other, origin, targetToken, room, event, "pong", 201, nil, targetGrant.InstallationID)
	queued := invoke(t, other, origin, commandPath(room, targetGrant.InstallationID), "ping", "before-disable-bot-2", 202)
	if _, err := i.s.db.Exec("INSERT INTO account_recovery_codes VALUES (?,randomblob(32),?)", account.ID, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM sessions", 3)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_installations", 4)
	for _, failAt := range []string{"DELETE ON bot_installations", "INSERT ON maintenance_audit"} {
		if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_fail_disable BEFORE ` + failAt + `
			BEGIN SELECT RAISE(ABORT,'fixture blocked suspension'); END`); err != nil {
			t.Fatal(err)
		}
		if err := DisableLocalAccount(t.Context(), i.cfg.Database, account.ID); err == nil || !strings.Contains(err.Error(), "fixture blocked suspension") {
			t.Fatalf("failed suspension returned %v", err)
		}
		call(t, target, origin, "GET", "/me", nil, 200)
		call(t, second, origin, "GET", "/me", nil, 200)
		checkBotToken(t, origin, targetToken, 200, nil)
		checkDBInt(t, i.s.db, "SELECT count(*) FROM account_recovery_codes", 1)
		checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_installations", 4)
		checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_events", 2)
		checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 0)
		if _, err := i.s.db.Exec("DROP TRIGGER fixture_fail_disable"); err != nil {
			t.Fatal(err)
		}
	}

	// Hold a real send after withUser succeeds, before its transaction can begin.
	blocking := &heldRequestBody{
		Reader: strings.NewReader(`{"markdown":"late message","requestId":"fixture-late-message"}`),
		read:   make(chan struct{}), resume: make(chan struct{}),
	}
	req := httptest.NewRequest("POST", origin+"/api/v1"+historyPath, blocking)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	for _, cookie := range target.Jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	done := make(chan int, 1)
	go func() {
		res := httptest.NewRecorder()
		i.s.ServeHTTP(res, req)
		done <- res.Code
	}()
	defer func() {
		if blocking.resume != nil {
			close(blocking.resume)
		}
	}()
	select {
	case <-blocking.read:
	case status := <-done:
		t.Fatalf("send returned %d before parsing the held body", status)
	case <-time.After(5 * time.Second):
		t.Fatal("send never reached the held body")
	}

	// Leave this SSE connection idle until its periodic session check.
	streamClient := *second
	streamClient.Timeout = 0
	streamContext, stopStream := context.WithTimeout(t.Context(), 22*time.Second)
	defer stopStream()
	streamRequest, err := http.NewRequestWithContext(streamContext, "GET", origin+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatalf("initial SSE status = %d", stream.StatusCode)
	}
	sse := bufio.NewReader(stream.Body)
	readSync(t, sse)
	for range 2 {
		if err := DisableLocalAccount(t.Context(), i.cfg.Database, account.ID); err != nil {
			t.Fatal(err)
		}
	}
	close(blocking.resume)
	blocking.resume = nil
	select {
	case status := <-done:
		if status != 401 {
			t.Fatalf("held send returned %d, want 401", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held send failed to terminate")
	}
	if tail, err := io.ReadAll(sse); err != nil || string(tail) != "data: {}\n\n" {
		t.Fatalf("idle SSE tail %q, error %v", tail, err)
	}

	call(t, target, origin, "GET", "/me", nil, 401)
	call(t, second, origin, "GET", historyPath, nil, 401)
	call(t, second, origin, "GET", "/events", nil, 401)
	call(t, target, origin, "POST", "/login", login, 401)
	checkBotToken(t, origin, targetToken, 401, nil)
	poll(t, origin, targetToken, room, targetGrant.InstallationID, "", 401, nil)
	postBotReply(t, other, origin, targetToken, room, queued, "late pong", 401, nil, targetGrant.InstallationID)
	call(t, other, origin, "GET", fmt.Sprintf("/rooms/%d/bots/%s/installation", peerRoom, targetBot.ID), nil, 404)
	call(t, other, origin, "PUT", fmt.Sprintf("/rooms/%d/bots/%s/installation", peerRoom, targetBot.ID),
		map[string][]string{"permissions": permissions}, 404)
	approve(other, pendingBot, pendingRequested, 404)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM sessions", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM account_recovery_codes", 0)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_installations", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_events", 0)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_command_requests", 0)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bots", 3)

	call(t, other, origin, "GET", "/me", nil, 200)
	var members []user
	call(t, other, origin, "GET", roomPath+"/members", nil, 200, &members)
	if len(members) != 2 {
		t.Fatal("suspension changed membership")
	}
	history := replyHistory(t, other, origin, historyPath, 200)
	if len(history) != 2 || history[0].Markdown != "Previous message" || history[1].Author["id"] != targetBot.ID {
		t.Fatalf("suspension changed previous messages: %+v", history)
	}
	call(t, other, origin, "POST", historyPath, map[string]string{"markdown": "Peer message", "requestId": "after-disable-human"}, 201)
	newEvent := invoke(t, other, origin, commandPath(room, peerGrant.InstallationID), "ping", "after-disable-bot-1", 202)
	postBotReply(t, other, origin, peerToken, room, newEvent, "still available", 201, nil, peerGrant.InstallationID)

	oldURL, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	i.host.Close()
	if err := i.s.Close(); err != nil {
		t.Fatal(err)
	}
	restart := newInstance(t, Config{Database: i.cfg.Database})
	old := browser(t)
	old.Jar.SetCookies(oldURL, target.Jar.Cookies(oldURL))
	call(t, old, restart.host.URL, "GET", "/me", nil, 401)
	call(t, old, restart.host.URL, "POST", "/login", login, 401)
}

type heldRequestBody struct {
	io.Reader
	read, resume chan struct{}
	started      bool
}

func (b *heldRequestBody) Read(p []byte) (int, error) {
	if !b.started {
		b.started = true
		resume := b.resume
		close(b.read)
		<-resume
	}
	return b.Reader.Read(p)
}

func TestDisableLocalAccountRequiresExistingCurrentDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "absent.sqlite")
	if err := DisableLocalAccount(t.Context(), file, 1); err == nil {
		t.Fatal("maintenance accepted a missing database")
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("maintenance created the missing database, stat error %v", err)
	}
	db, err := sqliteDatabase(file, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	if err := DisableLocalAccount(t.Context(), file, 1); err == nil || !strings.Contains(err.Error(), "数据库版本为 v1") {
		t.Fatalf("maintenance returned %v for an old database", err)
	}
	checkDBInt(t, db, "PRAGMA user_version", 1)
}

func TestInsertSessionRequiresCurrentPasswordAndActiveAccount(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "chat.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name,password,status)
		VALUES (1,'alice','Alice','new-hash','active'),(2,'bob','Bob','local-hash','recovery_required')`); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, attempt := range []struct {
		id   int64
		hash string
	}{{1, "old-hash"}, {2, "local-hash"}} {
		err := transaction(ctx, db, func(tx *sql.Tx) error {
			return insertSession(ctx, tx, attempt.id, randomText(32), &attempt.hash)
		})
		if !errors.Is(err, errAccountUnavailable) {
			t.Fatalf("login with stale hash or restricted state returned %v", err)
		}
	}
	checkDBInt(t, db, "SELECT count(*) FROM sessions", 0)
}
