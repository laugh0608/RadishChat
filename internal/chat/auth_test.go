package chat

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalRegistrationOptions(t *testing.T) {
	database := filepath.Join(t.TempDir(), "chat.sqlite")
	alice := credentials{Username: "alice", Password: "fixture-only-password"}
	for _, step := range []struct {
		name     string
		allow    bool
		username string
		register int
		login    int
	}{
		{"default_closed", false, "alice", http.StatusForbidden, http.StatusUnauthorized},
		{"explicitly_open", true, "alice", http.StatusCreated, http.StatusOK},
		{"closed_again", false, "other", http.StatusForbidden, http.StatusOK},
	} {
		t.Run(step.name, func(t *testing.T) {
			i := newInstance(t, Config{Database: database, AllowRegistration: step.allow})
			c := browser(t)
			var info struct{ Registration bool }
			call(t, c, i.host.URL, "GET", "/info", nil, http.StatusOK, &info)
			if info.Registration != step.allow {
				t.Fatalf("registration=%v, want %v", info.Registration, step.allow)
			}
			res := call(t, c, i.host.URL, "POST", "/register", credentials{Username: step.username, Password: alice.Password}, step.register)
			if !step.allow && len(res.Cookies()) != 0 {
				t.Fatal("closed registration issued a cookie")
			}
			call(t, browser(t), i.host.URL, "POST", "/login", alice, step.login)
			if step.username != alice.Username {
				call(t, c, i.host.URL, "POST", "/login", credentials{Username: step.username, Password: alice.Password}, http.StatusUnauthorized)
			}
		})
	}
}

func TestHTTPSLocalSession(t *testing.T) {
	proxy := httptest.NewUnstartedServer(nil)
	defer proxy.Close()
	origin := "https://" + proxy.Listener.Addr().String()
	s, err := New(Config{PublicURL: origin, Database: filepath.Join(t.TempDir(), "chat.sqlite"), AllowRegistration: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backend := httptest.NewServer(s)
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Config.Handler = &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target) }}
	proxy.StartTLS()
	c := browser(t)
	c.Transport = proxy.Client().Transport
	defer c.CloseIdleConnections()

	checkCookie := func(res *http.Response, age int) *http.Cookie {
		t.Helper()
		cookies := res.Cookies()
		if len(cookies) != 1 {
			t.Fatalf("got %d cookies, want 1", len(cookies))
		}
		cookie := cookies[0]
		if cookie.Name != "__Host-"+sessionCookie || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
			cookie.Domain != "" || cookie.Path != "/" || cookie.MaxAge != age {
			t.Fatal("unexpected HTTPS session cookie attributes")
		}
		return cookie
	}
	checkSession := func(method, path, from string, session *http.Cookie, status int) {
		t.Helper()
		req, err := http.NewRequest(method, origin+"/api/v1"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if from != "" {
			req.Header.Set("Origin", from)
		}
		req.AddCookie(session)
		res, err := proxy.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s %s: got %d, want %d", method, path, res.StatusCode, status)
		}
	}

	alice := credentials{Username: "alice", Password: "fixture-only-password"}
	age := int(sessionAge.Seconds())
	first := checkCookie(call(t, c, origin, "POST", "/register", alice, http.StatusCreated), age)
	call(t, c, origin, "GET", "/me", nil, http.StatusOK)
	second := checkCookie(call(t, c, origin, "POST", "/login", alice, http.StatusOK), age)
	checkSession("GET", "/me", "", first, http.StatusUnauthorized)
	checkSession("POST", "/logout", "https://other.example.test", second, http.StatusForbidden)
	checkSession("POST", "/logout", "", second, http.StatusForbidden)
	call(t, c, origin, "GET", "/me", nil, http.StatusOK)
	checkCookie(call(t, c, origin, "POST", "/logout", nil, http.StatusNoContent), -1)
	checkSession("GET", "/me", "", second, http.StatusUnauthorized)

	third := checkCookie(call(t, c, origin, "POST", "/login", alice, http.StatusOK), age)
	checkSession("GET", "/me", "", third, http.StatusOK)
	if _, err := s.db.Exec("UPDATE sessions SET expires=? WHERE token_hash=?", time.Now().Add(-time.Second).Unix(), digest(third.Value)); err != nil {
		t.Fatal(err)
	}
	checkSession("GET", "/me", "", third, http.StatusUnauthorized)
}

func TestLoginRateLimitBySource(t *testing.T) {
	i := newInstance(t, Config{AllowRegistration: true, TrustedProxyIP: "127.0.0.1"})
	addUser(t, i.host.URL, "alice")
	login := func(peer string, forwarded []string, body string, status int) {
		t.Helper()
		req := httptest.NewRequest("POST", i.host.URL+"/api/v1/login", strings.NewReader(body))
		req.RemoteAddr = netip.AddrPortFrom(netip.MustParseAddr(peer), 26001).String()
		req.Header.Set("Origin", i.host.URL)
		req.Header.Set("Content-Type", "application/json")
		for _, value := range forwarded {
			req.Header.Add("X-Forwarded-For", value)
		}
		w := httptest.NewRecorder()
		i.s.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("login status=%d, want %d", w.Code, status)
		}
	}

	for range 35 {
		login("127.0.0.1", []string{"192.0.2.10"}, "{}", http.StatusUnauthorized)
	}
	login("127.0.0.1", []string{"::ffff:192.0.2.10"}, "{}", http.StatusTooManyRequests)
	login("127.0.0.1", []string{"2001:db8::2"}, `{"username":"alice","password":"fixture-only-password"}`, http.StatusOK)

	for range 35 {
		login("192.0.2.20", []string{"192.0.2.23"}, "{}", http.StatusUnauthorized)
	}
	login("192.0.2.20", []string{"192.0.2.24"}, "{}", http.StatusTooManyRequests)
	for range 35 {
		login("127.0.0.1", nil, "{}", http.StatusUnauthorized)
	}
	login("127.0.0.1", []string{"192.0.2.23, 192.0.2.24"}, "{}", http.StatusTooManyRequests)
	login("127.0.0.1", []string{"192.0.2.23", "192.0.2.24"}, "{}", http.StatusTooManyRequests)
}
