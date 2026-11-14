package chat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

type instance struct {
	s    *Server
	cfg  Config
	host *httptest.Server
}

func newInstance(t *testing.T, cfg Config) instance {
	t.Helper()
	if cfg.Database == "" {
		cfg.Database = filepath.Join(t.TempDir(), "chat.sqlite")
	}
	host := httptest.NewUnstartedServer(nil)
	cfg.PublicURL = "http://" + host.Listener.Addr().String()
	s, err := New(cfg)
	if err != nil {
		host.Close()
		t.Fatal(err)
	}
	host.Config.Handler = s
	host.Start()
	t.Cleanup(func() { host.Close(); s.Close() })
	return instance{s, cfg, host}
}

func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func call(t *testing.T, c *http.Client, base, method, path string, input any, status int, parse ...any) *http.Response {
	t.Helper()
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+"/api/v1"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		message, _ := io.ReadAll(res.Body)
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, status, message)
	}
	for _, v := range parse {
		if err := json.NewDecoder(res.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

func addUser(t *testing.T, origin, name string) (*http.Client, user) {
	t.Helper()
	c := browser(t)
	var u user
	call(t, c, origin, "POST", "/register", credentials{Username: name, Password: "fixture-only-password"}, 201, &u)
	return c, u
}

func startStream(t *testing.T, origin string, c *http.Client) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	req, err := http.NewRequestWithContext(ctx, "GET", origin+"/api/v1/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	res, err := c.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stop := func() { cancel(); res.Body.Close() }
	t.Cleanup(stop)
	if res.StatusCode != 200 {
		stop()
		t.Fatalf("events status=%d", res.StatusCode)
	}
	return bufio.NewReader(res.Body), stop
}
func readSync(t *testing.T, r *bufio.Reader) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "event: sync\n" {
			return
		}
	}
}

func TestRoomsRealtimeAndPermissions(t *testing.T) {
	i := newInstance(t, Config{})
	a, alice := addUser(t, i.host.URL, "alice")
	b, bob := addUser(t, i.host.URL, "bob")
	c, _ := addUser(t, i.host.URL, "carol")
	stream, stop := startStream(t, i.host.URL, b)
	defer stop()
	readSync(t, stream)

	var result, fromPeer map[string]int64
	call(t, a, i.host.URL, "POST", "/rooms/direct", map[string]string{"username": "bob"}, 200, &result)
	readSync(t, stream)
	call(t, b, i.host.URL, "POST", "/rooms/direct", map[string]string{"username": "alice"}, 200, &fromPeer)
	if result["id"] != fromPeer["id"] {
		t.Fatal("direct room duplicated")
	}
	readSync(t, stream)
	direct := fmt.Sprintf("/rooms/%d/messages", result["id"])
	body := map[string]string{"markdown": "**你好**\n\n- 条目", "requestId": "stable_test_request"}
	var first, duplicate message
	call(t, a, i.host.URL, "POST", direct, body, 201, &first)
	readSync(t, stream)
	call(t, a, i.host.URL, "POST", direct, body, 200, &duplicate)
	if first.ID != duplicate.ID || first.Author.ID != alice.ID {
		t.Fatal("idempotency mismatch")
	}
	call(t, a, i.host.URL, "POST", direct, map[string]string{"markdown": "改了正文", "requestId": "stable_test_request"}, 409)
	var page messagePage
	call(t, b, i.host.URL, "GET", direct, nil, 200, &page)
	if len(page.Messages) != 1 || page.Messages[0].Markdown != body["markdown"] {
		t.Fatalf("unexpected history: %+v", page)
	}
	call(t, c, i.host.URL, "GET", direct, nil, 404)
	call(t, c, i.host.URL, "POST", direct, body, 404)

	call(t, a, i.host.URL, "POST", "/rooms/group", map[string]any{"name": "小组", "usernames": []string{"bob"}}, 201, &result)
	group := fmt.Sprintf("/rooms/%d", result["id"])
	call(t, b, i.host.URL, "POST", group+"/members", map[string]string{"username": "carol"}, 403)
	call(t, a, i.host.URL, "POST", group+"/messages", body, 201)
	call(t, a, i.host.URL, "POST", group+"/members", map[string]string{"username": "carol"}, 204)
	call(t, c, i.host.URL, "GET", group+"/messages", nil, 200, &page)
	if len(page.Messages) != 1 {
		t.Fatal("new member cannot see existing history")
	}
	call(t, c, i.host.URL, "POST", group+"/leave", nil, 204)
	call(t, c, i.host.URL, "GET", group+"/members", nil, 404)
	call(t, a, i.host.URL, "DELETE", fmt.Sprintf("%s/members/%d", group, bob.ID), nil, 204)
	call(t, b, i.host.URL, "GET", group+"/messages", nil, 404)
	call(t, b, i.host.URL, "POST", group+"/messages", body, 404)
	stop()
	call(t, b, i.host.URL, "POST", "/logout", nil, 204)
	call(t, b, i.host.URL, "GET", "/me", nil, 401)
	call(t, b, i.host.URL, "POST", "/login", credentials{Username: "bob", Password: "fixture-only-password"}, 200)
}

func TestDataPersistsAndInstanceOrigin(t *testing.T) {
	i := newInstance(t, Config{})
	c, _ := addUser(t, i.host.URL, "alice")
	var result map[string]int64
	call(t, c, i.host.URL, "POST", "/rooms/group", map[string]any{"name": "持久化"}, 201, &result)
	path := fmt.Sprintf("/rooms/%d/messages", result["id"])
	call(t, c, i.host.URL, "POST", path, map[string]string{"requestId": "persisted_message", "markdown": "仍然存在"}, 201)
	req, _ := http.NewRequest("POST", i.host.URL+"/api/v1/logout", nil)
	req.Header.Set("Origin", "https://other.example.test")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross-origin action accepted")
	}
	i.host.Close()
	i.s.Close()
	restarted := newInstance(t, i.cfg)
	d := browser(t)
	call(t, d, restarted.host.URL, "POST", "/login", credentials{Username: "alice", Password: "fixture-only-password"}, 200)
	var persisted messagePage
	call(t, d, restarted.host.URL, "GET", path, nil, 200, &persisted)
	if len(persisted.Messages) != 1 || persisted.Messages[0].Markdown != "仍然存在" {
		t.Fatal("message not restored")
	}
	other := newInstance(t, Config{})
	call(t, d, other.host.URL, "GET", "/me", nil, 401)
}

func TestPagination(t *testing.T) {
	i := newInstance(t, Config{})
	c, _ := addUser(t, i.host.URL, "alice")
	var result map[string]int64
	call(t, c, i.host.URL, "POST", "/rooms/group", map[string]any{"name": "分页"}, 201, &result)
	path := fmt.Sprintf("/rooms/%d/messages", result["id"])
	for j := 1; j <= 55; j++ {
		call(t, c, i.host.URL, "POST", path, map[string]string{"requestId": fmt.Sprintf("pagination_msg_%d", j), "markdown": fmt.Sprint(j)}, 201)
	}
	var page messagePage
	call(t, c, i.host.URL, "GET", path, nil, 200, &page)
	if !page.More || len(page.Messages) != 50 || page.Messages[0].Markdown != "6" {
		t.Fatalf("wrong last page")
	}
	call(t, c, i.host.URL, "GET", fmt.Sprintf("%s?before=%d", path, page.Messages[0].ID), nil, 200, &page)
	if page.More || len(page.Messages) != 5 || page.Messages[0].Markdown != "1" || page.Messages[4].Markdown != "5" {
		t.Fatalf("wrong previous page")
	}
	call(t, c, i.host.URL, "GET", fmt.Sprintf("%s?after=%d", path, page.Messages[0].ID), nil, 200, &page)
	if !page.More || len(page.Messages) != 50 || page.Messages[0].Markdown != "2" || page.Messages[49].Markdown != "51" {
		t.Fatalf("wrong incremental page")
	}
}

func TestRadishOIDCBrowserFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	type flowData struct{ nonce, challenge, redirect string }
	var mu sync.Mutex
	var config flowData
	var expectedNonceBad bool
	var issuer string
	mux := http.NewServeMux()
	provider := httptest.NewUnstartedServer(mux)
	issuer = "http://" + provider.Listener.Addr().String()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id, secret, _ := r.BasicAuth()
		if r.ParseForm() != nil || id != "test-app" || secret != "fixture" || r.Form.Get("code") != "issuer_code" || r.Form.Get("redirect_uri") != config.redirect ||
			oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != config.challenge {
			fail(w, 400, "invalid fixture request")
			return
		}
		nonce := config.nonce
		if expectedNonceBad {
			nonce = "different"
		}
		now := time.Now()
		claims, _ := json.Marshal(map[string]any{"iss": issuer, "sub": "radish-user", "aud": "test-app", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "nonce": nonce,
			"name": "Alice via Radish", "preferred_username": "alice"})
		signed, _ := signer.Sign(claims)
		compact, _ := signed.CompactSerialize()
		reply(w, 200, map[string]string{"access_token": "unused", "token_type": "Bearer", "id_token": compact})
	})
	provider.Start()
	defer provider.Close()
	i := newInstance(t, Config{OIDCIssuer: issuer, OIDCClientID: "test-app", OIDCSecret: "fixture"})
	_, local := addUser(t, i.host.URL, "alice")
	client := browser(t)
	begin := func() string {
		var start map[string]string
		call(t, client, i.host.URL, "POST", "/auth/radish/start", nil, 200, &start)
		u, err := url.Parse(start["authorizationUrl"])
		if err != nil || u.Host != strings.TrimPrefix(issuer, "http://") {
			t.Fatal("invalid provider URL")
		}
		v := u.Query()
		mu.Lock()
		defer mu.Unlock()
		config = flowData{v.Get("nonce"), v.Get("code_challenge"), v.Get("redirect_uri")}
		if v.Get("code_challenge_method") != "S256" || config.nonce == "" || config.challenge == "" || !strings.HasPrefix(config.redirect, i.host.URL) {
			t.Fatal("missing proof")
		}
		return "/auth/radish/callback?code=issuer_code&state=" + v.Get("state")
	}
	redirect := func(c *http.Client, path, want string) {
		res := call(t, c, i.host.URL, "GET", path, nil, 303)
		if res.Header.Get("Location") != want {
			t.Fatalf("location %q want %q", res.Header.Get("Location"), want)
		}
	}
	path := begin()
	redirect(browser(t), path, "/?login=invalid")
	redirect(client, path, "/")
	var first, second user
	call(t, client, i.host.URL, "GET", "/me", nil, 200, &first)
	if first.ID == local.ID || first.DisplayName != "Alice via Radish" {
		t.Fatal("external identity merged with local account")
	}
	redirect(client, path, "/?login=invalid")
	redirect(client, begin(), "/")
	call(t, client, i.host.URL, "GET", "/me", nil, 200, &second)
	if first != second {
		t.Fatal("external identity not stable")
	}
	mu.Lock()
	expectedNonceBad = true
	mu.Unlock()
	redirect(client, begin(), "/?login=invalid")
}
