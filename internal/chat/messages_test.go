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
