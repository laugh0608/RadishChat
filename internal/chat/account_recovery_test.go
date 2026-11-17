package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const recoveryPath = "/auth/local/recover"
const replacementPassword = "fixture-replacement-password"

func recoverTestAccount(t *testing.T, client *http.Client, origin string, input localRecoveryRequest, status int) {
	t.Helper()
	var body map[string]string
	var response *http.Response
	if status == 204 {
		response = call(t, client, origin, "POST", recoveryPath, input, status)
	} else {
		response = call(t, client, origin, "POST", recoveryPath, input, status, &body)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Set-Cookie") != "" {
		t.Fatal("recovery response is cacheable or sets a cookie")
	}
	if status == 401 && body["error"] != invalidRecoveryMessage {
		t.Fatalf("unexpected invalid recovery response: %v", body)
	}
}

func issueTestRecovery(t *testing.T, instance instance, id int64, reopen bool) string {
	t.Helper()
	code, err := IssueLocalAccountRecovery(t.Context(), instance.cfg.Database, id, reopen)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestLocalAccountRecoveryLifecycle(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin := i.host.URL
	old, account := addUser(t, origin, "alice")
	peer, _ := addUser(t, origin, "peer")
	roomID := createTestGroup(t, old, origin, "Recovery group", "peer")
	var before []room
	call(t, old, origin, "GET", "/rooms", nil, 200, &before)
	if len(before) != 1 {
		t.Fatal("recovery fixture must have one room")
	}
	bot := createTestBot(t, old, origin, "Recovery fixture Bot")
	token := rotateTestBot(t, old, origin, bot.ID)
	install := installTestBot(t, old, origin, fmt.Sprintf("/rooms/%d/bots/%s/installation", roomID, bot.ID))

	if code, err := IssueLocalAccountRecovery(t.Context(), i.cfg.Database, account.ID, true); err == nil || code != "" {
		t.Fatal("reopen accepted an active account")
	}
	if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_fail_issue BEFORE INSERT ON account_recovery_codes
		BEGIN SELECT RAISE(ABORT,'fixture blocked issuance'); END`); err != nil {
		t.Fatal(err)
	}
	if code, err := IssueLocalAccountRecovery(t.Context(), i.cfg.Database, account.ID, false); err == nil || code != "" {
		t.Fatal("failed issuance returned a code")
	}
	call(t, old, origin, "GET", "/me", nil, 200)
	if _, err := i.s.db.Exec("DROP TRIGGER fixture_fail_issue"); err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now()
	firstCode := issueTestRecovery(t, i, account.ID, false)
	var stored []byte
	var expiration int64
	if err := i.s.db.QueryRow("SELECT token_hash,expires FROM account_recovery_codes WHERE user_id=?", account.ID).Scan(&stored, &expiration); err != nil {
		t.Fatal(err)
	}
	if len(firstCode) != 43 || !bytes.Equal(stored, digest(firstCode)) || expiration < issuedAt.Add(15*time.Minute).Unix() || expiration > time.Now().Add(15*time.Minute).Unix() {
		t.Fatal("issued code or expiration does not match its contract")
	}
	call(t, old, origin, "GET", "/me", nil, 401)
	call(t, peer, origin, "GET", "/me", nil, 200)
	checkBotToken(t, origin, token, 200, nil)
	testBotRoomAccess(t, origin, token, roomID, install.InstallationID, 200)
	credentialsBefore := credentials{Username: account.Username, Password: "fixture-only-password"}
	requester := browser(t)
	call(t, requester, origin, "POST", "/login", credentialsBefore, 401)
	if _, err := i.s.db.Exec("UPDATE account_recovery_codes SET expires=unixepoch() WHERE user_id=?", account.ID); err != nil {
		t.Fatal(err)
	}
	attempt := localRecoveryRequest{Username: "  ALICE  ", RecoveryCode: firstCode, NewPassword: replacementPassword}
	recoverTestAccount(t, requester, origin, attempt, 401)
	attempt.RecoveryCode = issueTestRecovery(t, i, account.ID, false)
	if attempt.RecoveryCode == firstCode {
		t.Fatal("reissue did not replace the code")
	}
	invalid := attempt
	invalid.RecoveryCode = firstCode
	recoverTestAccount(t, requester, origin, invalid, 401)
	invalid = attempt
	invalid.Username = "unknown"
	recoverTestAccount(t, requester, origin, invalid, 401)
	invalid.Username = "peer"
	recoverTestAccount(t, requester, origin, invalid, 401)
	invalid = attempt
	invalid.NewPassword = "short"
	recoverTestAccount(t, requester, origin, invalid, 400)
	invalid.NewPassword = credentialsBefore.Password
	recoverTestAccount(t, requester, origin, invalid, 400)

	encoded, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	wrongOrigin := httptest.NewRequest(http.MethodPost, origin+"/api/v1"+recoveryPath, bytes.NewReader(encoded))
	wrongOrigin.Header.Set("Origin", "https://other.example")
	wrongOrigin.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	i.s.ServeHTTP(response, wrongOrigin)
	if response.Code != 403 {
		t.Fatalf("cross-origin recovery returned %d", response.Code)
	}

	if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_fail_consume BEFORE DELETE ON account_recovery_codes
		BEGIN SELECT RAISE(ABORT,'fixture blocked consumption'); END`); err != nil {
		t.Fatal(err)
	}
	recoverTestAccount(t, requester, origin, attempt, 500)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users WHERE username='alice' AND status='recovery_required'", 1)
	if _, err := i.s.db.Exec("DROP TRIGGER fixture_fail_consume"); err != nil {
		t.Fatal(err)
	}

	// Send two real requests with the same proof at once, then verify which commits took effect.
	start := make(chan struct{})
	results := make(chan int, 2)
	for range 2 {
		req, err := http.NewRequestWithContext(t.Context(), "POST", origin+"/api/v1"+recoveryPath, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		go func() {
			<-start
			res, err := requester.Do(req)
			if err != nil {
				results <- 0
				return
			}
			defer res.Body.Close()
			if res.Header.Get("Set-Cookie") != "" {
				results <- 0
				return
			}
			results <- res.StatusCode
		}()
	}
	close(start)
	codes := []int{<-results, <-results}
	slices.Sort(codes)
	if !slices.Equal(codes, []int{204, 401}) {
		t.Fatalf("concurrent recovery results %v", codes)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM account_recovery_codes", 0)
	recoverTestAccount(t, requester, origin, attempt, 401)
	call(t, requester, origin, "GET", "/me", nil, 401)
	call(t, old, origin, "GET", "/me", nil, 401)
	call(t, requester, origin, "POST", "/login", credentialsBefore, 401)
	var recovered user
	call(t, requester, origin, "POST", "/login", credentials{Username: account.Username, Password: replacementPassword}, 200, &recovered)
	var after []room
	call(t, requester, origin, "GET", "/rooms", nil, 200, &after)
	if recovered != account || !slices.Equal(after, before) {
		t.Fatal("recovery changed identity or room membership")
	}
	testBotRoomAccess(t, origin, token, roomID, install.InstallationID, 200)
}

func TestLocalAccountReopeningAndSnapshotRecovery(t *testing.T) {
	a := newInstance(t, Config{Registration: RegistrationOpen})
	b := newInstance(t, Config{Registration: RegistrationOpen})
	old, account := addUser(t, a.host.URL, "alice")
	_, other := addUser(t, b.host.URL, "alice")
	if account.ID != other.ID {
		t.Fatal("fixture needs the same account ID in two independent instances")
	}
	activeSnapshot := filepath.Join(t.TempDir(), "active.sqlite")
	if err := CopyDatabase(t.Context(), a.cfg.Database, activeSnapshot); err != nil {
		t.Fatal(err)
	}
	firstCode := issueTestRecovery(t, a, account.ID, false)
	bCode := issueTestRecovery(t, b, other.ID, false)
	client := browser(t)
	input := localRecoveryRequest{Username: account.Username, RecoveryCode: firstCode, NewPassword: replacementPassword}
	recoverTestAccount(t, client, b.host.URL, input, 401)
	if err := DisableLocalAccount(t.Context(), a.cfg.Database, account.ID); err != nil {
		t.Fatal(err)
	}
	recoverTestAccount(t, old, a.host.URL, input, 401)
	if code, err := IssueLocalAccountRecovery(t.Context(), a.cfg.Database, account.ID, false); err == nil || code != "" {
		t.Fatal("ordinary issuance bypassed suspension")
	}
	input.RecoveryCode = issueTestRecovery(t, a, account.ID, true)
	if code, err := IssueLocalAccountRecovery(t.Context(), a.cfg.Database, account.ID, true); err == nil || code != "" {
		t.Fatal("reopen accepted an account already awaiting recovery")
	}
	pendingSnapshot := filepath.Join(t.TempDir(), "pending.sqlite")
	if err := CopyDatabase(t.Context(), a.cfg.Database, pendingSnapshot); err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := CopyDatabase(t.Context(), pendingSnapshot, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored := newInstance(t, Config{Database: restoredPath})
	recoverTestAccount(t, client, restored.host.URL, input, 401)
	checkDBInt(t, restored.s.db, "SELECT count(*) FROM users WHERE status='recovery_required'", 1)
	recoverTestAccount(t, client, a.host.URL, input, 204)
	input.RecoveryCode = issueTestRecovery(t, restored, account.ID, false)
	recoverTestAccount(t, client, restored.host.URL, input, 204)

	// Reapply the suspension when the restored snapshot predates it.
	olderPath := filepath.Join(t.TempDir(), "older.sqlite")
	if err := CopyDatabase(t.Context(), activeSnapshot, olderPath); err != nil {
		t.Fatal(err)
	}
	if err := DisableLocalAccount(t.Context(), olderPath, account.ID); err != nil {
		t.Fatal(err)
	}
	older := newInstance(t, Config{Database: olderPath})
	login := credentials{Username: account.Username, Password: "fixture-only-password"}
	call(t, client, older.host.URL, "POST", "/login", login, 401)
	input.RecoveryCode = issueTestRecovery(t, older, account.ID, true)
	call(t, client, older.host.URL, "POST", "/login", login, 401)
	recoverTestAccount(t, client, older.host.URL, input, 204)
	login.Password = replacementPassword
	var recovered user
	call(t, client, older.host.URL, "POST", "/login", login, 200, &recovered)
	if recovered != account {
		t.Fatal("reopening after restore changed account identity")
	}
	input.RecoveryCode = bCode
	recoverTestAccount(t, browser(t), b.host.URL, input, 204)
}

func TestLocalRecoveryRateLimitsSourceAndNormalizedUsername(t *testing.T) {
	i := newInstance(t, Config{})
	post := func(source, username string, status int) {
		t.Helper()
		payload, err := json.Marshal(localRecoveryRequest{Username: username})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", i.host.URL+"/api/v1"+recoveryPath, strings.NewReader(string(payload)))
		r.RemoteAddr = source
		r.Header.Set("Origin", i.host.URL)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		i.s.ServeHTTP(out, r)
		if out.Code != status || (status == 429 && out.Header().Get("Retry-After") != "60") {
			t.Fatalf("recovery returned %d for %s from %s, want %d", out.Code, username, source, status)
		}
	}
	for n := range 12 {
		post(fmt.Sprintf("192.0.2.%d:80", n+1), "  ALICE  ", 401)
	}
	post("192.0.2.100:80", "alice", 429)
	for n := range 35 {
		post("192.0.2.101:80", fmt.Sprintf("unique-%d", n), 401)
	}
	post("192.0.2.101:80", "unique-last", 429)
}
