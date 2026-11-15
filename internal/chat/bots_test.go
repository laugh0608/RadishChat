package chat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func createTestBot(t *testing.T, client *http.Client, origin, name string) bot {
	t.Helper()
	var record bot
	call(t, client, origin, http.MethodPost, "/bots", map[string]string{"displayName": name}, http.StatusCreated, &record)
	if record.ID == "" || record.DisplayName != strings.TrimSpace(name) {
		t.Fatal("unexpected Bot definition")
	}
	return record
}

func rotateTestBot(t *testing.T, client *http.Client, origin, id string) string {
	t.Helper()
	var response map[string]string
	res := call(t, client, origin, http.MethodPost, "/bots/"+id+"/credential/rotate", nil, http.StatusOK, &response)
	token := response["token"]
	if len(response) != 1 || !strings.HasPrefix(token, botTokenPrefix) || len(token) != 47 ||
		res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("rotation must return a single Bot token with no-store")
	}
	return token
}

func botRequest(t *testing.T, client *http.Client, origin, method, path string, authorization []string, status int, dest any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, origin+"/api/v1"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	for _, value := range authorization {
		req.Header.Add("Authorization", value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d", method, strings.SplitN(path, "?", 2)[0], res.StatusCode, status)
	}
	if status == http.StatusUnauthorized && strings.HasPrefix(path, "/bot/me") &&
		!strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer ") {
		t.Fatal("missing Bearer challenge")
	}
	if dest != nil {
		if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
			t.Fatal(err)
		}
	}
}

func checkBotToken(t *testing.T, origin, token string, status int, dest any) {
	t.Helper()
	botRequest(t, browser(t), origin, http.MethodGet, "/bot/me", []string{"Bearer " + token}, status, dest)
}

func TestBotIdentityAndCredentialLifecycle(t *testing.T) {
	local := newInstance(t, Config{Registration: RegistrationOpen})
	origin := local.host.URL
	alice, _ := addUser(t, origin, "alice")
	bob, _ := addUser(t, origin, "bob")
	anonymous := browser(t)
	call(t, anonymous, origin, http.MethodGet, "/bots", nil, http.StatusUnauthorized)
	call(t, alice, origin, http.MethodPost, "/bots", map[string]string{"displayName": " "}, http.StatusBadRequest)
	record := createTestBot(t, alice, origin, "  Fixture Bot  ")
	bobsBot := createTestBot(t, bob, origin, "Fixture Bot")
	if record.ID == bobsBot.ID {
		t.Fatal("Bots with the same display name must have distinct identities")
	}

	path := "/bots/" + record.ID + "/credential"
	call(t, bob, origin, http.MethodPost, path+"/rotate", nil, http.StatusNotFound)
	call(t, bob, origin, http.MethodDelete, path, nil, http.StatusNotFound)
	wrongOrigin, err := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+"/api/v1"+path+"/rotate", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongOrigin.Header.Set("Origin", "https://other.example.test")
	res, err := alice.Do(wrongOrigin)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin rotation returned %d", res.StatusCode)
	}

	first := rotateTestBot(t, alice, origin, record.ID)
	var identity map[string]string
	checkBotToken(t, origin, first, http.StatusOK, &identity)
	if len(identity) != 2 || identity["id"] != record.ID || identity["displayName"] != record.DisplayName {
		t.Fatal("Bot identity must contain only its own ID and display name")
	}
	var own []map[string]string
	call(t, alice, origin, http.MethodGet, "/bots", nil, http.StatusOK, &own)
	if len(own) != 1 || len(own[0]) != 2 || own[0]["id"] != record.ID || own[0]["displayName"] != record.DisplayName {
		t.Fatal("list exposed another Bot or credential fields")
	}

	var stored []byte
	if err := local.s.db.QueryRow("SELECT token_hash FROM bots WHERE id=?", record.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, digest(first)) {
		t.Fatal("credential was not stored as a digest")
	}
	checkDBInt(t, local.s.db, "SELECT count(*) FROM users", 2)
	checkDBInt(t, local.s.db, "SELECT count(*) FROM members", 0)

	serviceURL, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	humanCookies := alice.Jar.Cookies(serviceURL)
	if len(humanCookies) != 1 {
		t.Fatal("expected a human session cookie")
	}
	botRequest(t, alice, origin, http.MethodGet, "/bot/me", nil, http.StatusUnauthorized, nil)
	botRequest(t, anonymous, origin, http.MethodGet, "/bot/me?access_token="+url.QueryEscape(first), nil, http.StatusUnauthorized, nil)
	checkBotToken(t, origin, humanCookies[0].Value, http.StatusUnauthorized, nil)
	botRequest(t, anonymous, origin, http.MethodGet, "/bot/me", []string{"Bearer " + first, "Bearer " + first}, http.StatusUnauthorized, nil)
	for _, endpoint := range []string{"/me", "/bots", "/rooms", "/events"} {
		botRequest(t, anonymous, origin, http.MethodGet, endpoint, []string{"Bearer " + first}, http.StatusUnauthorized, nil)
	}
	botRequest(t, anonymous, origin, http.MethodPost, path+"/rotate", []string{"Bearer " + first}, http.StatusUnauthorized, nil)
	other := newInstance(t, Config{})
	checkBotToken(t, other.host.URL, first, http.StatusUnauthorized, nil)

	if _, err := local.s.db.Exec("CREATE TRIGGER fail_rotate BEFORE UPDATE OF token_hash ON bots BEGIN SELECT RAISE(ABORT,'fixture blocked rotation'); END"); err != nil {
		t.Fatal(err)
	}
	call(t, alice, origin, http.MethodPost, path+"/rotate", nil, http.StatusInternalServerError)
	checkBotToken(t, origin, first, http.StatusOK, nil)
	if _, err := local.s.db.Exec("DROP TRIGGER fail_rotate"); err != nil {
		t.Fatal(err)
	}
	call(t, alice, origin, http.MethodPost, path+"/rotate", nil, http.StatusOK) // Discard the response, as after a lost confirmation.
	checkBotToken(t, origin, first, http.StatusUnauthorized, nil)
	recovered := rotateTestBot(t, alice, origin, record.ID)
	checkBotToken(t, origin, recovered, http.StatusOK, nil)
	call(t, alice, origin, http.MethodDelete, path, nil, http.StatusNoContent)
	call(t, alice, origin, http.MethodDelete, path, nil, http.StatusNoContent)
	checkBotToken(t, origin, recovered, http.StatusUnauthorized, nil)
}

func TestConcurrentBotCredentialRotationLeavesOneValidToken(t *testing.T) {
	instance := newInstance(t, Config{Registration: RegistrationOpen})
	origin := instance.host.URL
	owner, _ := addUser(t, origin, "owner")
	record := createTestBot(t, owner, origin, "Fixture Bot")

	tokens := make([]string, 4)
	t.Run("rotations", func(t *testing.T) {
		for i := range tokens {
			t.Run(string(rune('A'+i)), func(t *testing.T) {
				t.Parallel()
				tokens[i] = rotateTestBot(t, owner, origin, record.ID)
			})
		}
	})
	var stored []byte
	if err := instance.s.db.QueryRow("SELECT token_hash FROM bots WHERE id=?", record.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, token := range tokens {
		if bytes.Equal(stored, digest(token)) {
			active++
			checkBotToken(t, origin, token, http.StatusOK, nil)
		} else {
			checkBotToken(t, origin, token, http.StatusUnauthorized, nil)
		}
	}
	if active != 1 {
		t.Fatalf("%d credentials match the last committed rotation", active)
	}
}
