package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPollPreservesCursorWhenOnlyPartOfPageIsConfirmed(t *testing.T) {
	var replies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			if r.URL.Query().Get("after") != "saved" {
				t.Errorf("after = %q, want saved", r.URL.Query().Get("after"))
			}
			fmt.Fprint(w, `{"events":[
				{"id":"first","type":"command.requested","version":1,"payload":{"command":"ping"}},
				{"id":"second","type":"command.requested","version":1,"payload":{"command":"ping"}}
			],"nextCursor":"second","hasMore":false}`)
		case "/events/first/reply":
			replies = append(replies, "first")
			w.WriteHeader(http.StatusOK)
		case "/events/second/reply":
			replies = append(replies, "second")
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "position.cursor")
	if err := saveCursor(path, "saved"); err != nil {
		t.Fatal(err)
	}
	bot := pingBot{client: server.Client(), eventsURL: server.URL + "/events", cursorPath: path}
	if err := bot.poll(t.Context(), "saved", ""); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("poll error = %v, want 401", err)
	}
	server.Close()
	if got := strings.Join(replies, ","); got != "first,second" {
		t.Fatalf("replies = %s, want first,second", got)
	}
	if data, err := os.ReadFile(path); string(data) != "saved" || err != nil {
		t.Fatalf("cursor = %q, err = %v, want saved", data, err)
	}
}

func TestPollRequiresConfirmationOfCurrentGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("after") {
		case "saved":
			w.WriteHeader(http.StatusGone)
			fmt.Fprint(w, `{"resumeAfter":"current-boundary"}`)
		case "current-boundary":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected after = %q", r.URL.Query().Get("after"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "position.cursor")
	bot := pingBot{client: server.Client(), eventsURL: server.URL + "/events", cursorPath: path}
	for _, confirmation := range []string{"", "old-boundary"} {
		if err := bot.poll(t.Context(), "saved", confirmation); err == nil || !strings.Contains(err.Error(), `-resume-after "current-boundary"`) {
			t.Fatalf("poll error = %v, want current gap confirmation", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unconfirmed gap wrote a cursor or returned an unexpected error: %v", err)
		}
	}
	if err := bot.poll(t.Context(), "saved", "current-boundary"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("poll error = %v, want 404 after resumption", err)
	}
	if data, err := os.ReadFile(path); string(data) != "current-boundary" || err != nil {
		t.Fatalf("cursor = %q, err = %v, want current-boundary", data, err)
	}
}
