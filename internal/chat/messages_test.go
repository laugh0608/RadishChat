package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMessageConfirmationAndRetryAfterLostResponse(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	b, bob := addUser(t, i.host.URL, "bob")
	var group struct{ ID int64 }
	call(t, a, i.host.URL, "POST", "/rooms/group", map[string]any{"name": "重试验证", "usernames": []string{"bob"}}, http.StatusCreated, &group)
	roomPath := fmt.Sprintf("/rooms/%d", group.ID)
	path := roomPath + "/messages"
	body := map[string]string{"markdown": "消息已保存", "requestId": "fixture_response_lost"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	loss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let the real handler finish committing, then close the connection without sending its reply.
		result := httptest.NewRecorder()
		i.s.ServeHTTP(result, r)
		if result.Code != http.StatusCreated {
			t.Errorf("first send returned %d: %s", result.Code, result.Body)
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer loss.Close()
	req, err := http.NewRequest(http.MethodPost, loss.URL+"/api/v1"+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", i.host.URL)
	req.Header.Set("Content-Type", "application/json")
	response, err := b.Do(req)
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected lost confirmation, got %v", err)
	}
	var history messagePage
	call(t, a, i.host.URL, "GET", path, nil, http.StatusOK, &history)
	if len(history.Messages) != 1 || history.Messages[0].Author != bob || history.Messages[0].Markdown != body["markdown"] {
		t.Fatalf("confirmation was lost but the expected message is missing: %+v", history)
	}
	committed := history.Messages[0]
	var retried message
	call(t, b, i.host.URL, "POST", path, body, http.StatusOK, &retried)
	if retried != committed {
		t.Fatalf("retry got %+v, want %+v", retried, committed)
	}
	call(t, b, i.host.URL, "POST", path, map[string]string{"markdown": "修改正文", "requestId": body["requestId"]}, http.StatusConflict)

	origin, err := url.Parse(i.host.URL)
	if err != nil {
		t.Fatal(err)
	}
	stale := browser(t)
	stale.Jar.SetCookies(origin, b.Jar.Cookies(origin))
	call(t, b, i.host.URL, "POST", "/logout", nil, http.StatusNoContent)
	call(t, stale, i.host.URL, "POST", path, body, http.StatusUnauthorized)
	call(t, b, i.host.URL, "POST", "/login", credentials{Username: "bob", Password: "fixture-only-password"}, http.StatusOK)
	call(t, a, i.host.URL, "DELETE", fmt.Sprintf("%s/members/%d", roomPath, bob.ID), nil, http.StatusNoContent)
	call(t, b, i.host.URL, "POST", path, body, http.StatusNotFound)
	call(t, a, i.host.URL, "GET", path, nil, http.StatusOK, &history)
	if !slices.Equal(history.Messages, []message{committed}) {
		t.Fatalf("retry changed stored history: %+v", history)
	}
}

func TestFailedMessageWriteIsNotConfirmed(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	var group struct{ ID int64 }
	call(t, a, i.host.URL, "POST", "/rooms/group", map[string]string{"name": "写入失败"}, http.StatusCreated, &group)
	path := fmt.Sprintf("/rooms/%d/messages", group.ID)
	if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_stop_message BEFORE INSERT ON messages
		BEGIN SELECT RAISE(ABORT, 'fixture blocked write'); END`); err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"markdown": "写入恢复", "requestId": "fixture_blocked_write"}
	call(t, a, i.host.URL, "POST", path, body, http.StatusInternalServerError)
	var history messagePage
	call(t, a, i.host.URL, "GET", path, nil, http.StatusOK, &history)
	if len(history.Messages) != 0 {
		t.Fatalf("failed write left a message: %+v", history)
	}
	if _, err := i.s.db.Exec("DROP TRIGGER fixture_stop_message"); err != nil {
		t.Fatal(err)
	}
	call(t, a, i.host.URL, "POST", path, body, http.StatusCreated)
}

func TestMarkdownOriginalAndCodePointLimit(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	b, _ := addUser(t, i.host.URL, "bob")
	var direct struct{ ID int64 }
	call(t, a, i.host.URL, "POST", "/rooms/direct", map[string]string{"username": "bob"}, http.StatusOK, &direct)
	path := fmt.Sprintf("/rooms/%d/messages", direct.ID)
	prefix := "\n- **列表**\n\n```text\n<fixture>\n```\n\n"
	markdown := prefix + strings.Repeat("😀", 7999-utf8.RuneCountInString(prefix)) + "\n"
	body := map[string]string{"markdown": markdown + "😀", "requestId": "fixture_markdown_boundary"}
	call(t, a, i.host.URL, "POST", path, body, http.StatusBadRequest)
	body["markdown"] = markdown
	var sent message
	call(t, a, i.host.URL, "POST", path, body, http.StatusCreated, &sent)
	var history messagePage
	call(t, b, i.host.URL, "GET", path, nil, http.StatusOK, &history)
	if sent.Markdown != markdown || !slices.Equal(history.Messages, []message{sent}) {
		t.Fatal("8,000-code-point Markdown did not round trip unchanged")
	}
}

func TestPagingAcrossReconnectAndRejoin(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	b, _ := addUser(t, i.host.URL, "bob")
	origin := i.host.URL
	var group, other struct{ ID int64 }
	call(t, a, origin, "POST", "/rooms/group", map[string]any{"name": "游标合成群", "usernames": []string{"bob"}}, http.StatusCreated, &group)
	call(t, a, origin, "POST", "/rooms/direct", map[string]string{"username": "bob"}, http.StatusOK, &other)
	roomPath := fmt.Sprintf("/rooms/%d", group.ID)
	path := roomPath + "/messages"
	var sent []message
	write := func(count int) {
		t.Helper()
		for range count {
			key := fmt.Sprintf("paging_fixture_%03d", len(sent)+1)
			var saved message
			call(t, a, origin, "POST", path, map[string]string{"markdown": key, "requestId": key}, http.StatusCreated, &saved)
			sent = append(sent, saved)
		}
	}
	read := func(query string, start, end int, more bool) {
		t.Helper()
		var page messagePage
		call(t, b, origin, "GET", path+query, nil, http.StatusOK, &page)
		if !slices.Equal(page.Messages, sent[start:end]) || page.More != more {
			t.Fatalf("%s: got %d messages, more=%t, want messages %d through %d, more=%t",
				query, len(page.Messages), page.More, start+1, end, more)
		}
	}
	checkFrame := func(stream io.Reader, want string) {
		t.Helper()
		frame := make([]byte, len(want))
		if _, err := io.ReadFull(stream, frame); err != nil || string(frame) != want {
			t.Fatalf("SSE frame %q, want %q (error %v)", frame, want, err)
		}
	}

	read("", 0, 0, false)
	stream, disconnect := startStream(t, origin, b)
	checkFrame(stream, "retry: 2500\nevent: sync\ndata: {}\n\n")
	disconnect()
	write(100)
	read("?after=0", 0, 50, true)
	read("?after="+fmt.Sprint(sent[49].ID), 50, 100, false)
	read("", 50, 100, true)
	call(t, a, origin, "POST", fmt.Sprintf("/rooms/%d/messages", other.ID),
		map[string]string{"markdown": "另一个房间", "requestId": "fixture_other_room"}, http.StatusCreated)
	write(105)
	stream, disconnect = startStream(t, origin, b)
	checkFrame(stream, "retry: 2500\nevent: sync\ndata: {}\n\n")
	read("?after="+fmt.Sprint(sent[99].ID), 100, 150, true)
	write(2)
	checkFrame(stream, "event: sync\ndata: {}\n\n")
	read("?before="+fmt.Sprint(sent[50].ID), 0, 50, false)
	read("?after="+fmt.Sprint(sent[149].ID), 150, 200, true)
	write(2)
	read("?after="+fmt.Sprint(sent[199].ID), 200, 209, false)
	read("?after="+fmt.Sprint(sent[208].ID), 209, 209, false)
	disconnect()

	call(t, b, origin, "POST", roomPath+"/leave", nil, http.StatusNoContent)
	write(1)
	call(t, b, origin, "GET", path+"?after=0", nil, http.StatusNotFound)
	call(t, a, origin, "POST", roomPath+"/members", map[string]string{"username": "bob"}, http.StatusNoContent)
	write(53)
	read("?after="+fmt.Sprint(sent[99].ID), 210, 260, true)
	read("?after="+fmt.Sprint(sent[259].ID), 260, 263, false)
	read("?before="+fmt.Sprint(sent[210].ID), 210, 210, false)
	for _, invalid := range []string{"?before=0", "?after=", "?after=-1", "?before=5&after=0"} {
		call(t, b, origin, "GET", path+invalid, nil, http.StatusBadRequest)
	}
}
