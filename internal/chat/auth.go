package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)
var inviteCodePattern = regexp.MustCompile(`^[a-f0-9]{48}$`)

type RegistrationMode string

const (
	RegistrationOff    RegistrationMode = "off"
	RegistrationOpen   RegistrationMode = "on"
	RegistrationInvite RegistrationMode = "invite"
)

const sessionCookie = "radishchat_session"
const sessionAge = 7 * 24 * time.Hour

type user struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}
type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName,omitempty"`
}
type registrationRequest struct {
	credentials
	InviteCode string `json:"inviteCode,omitempty"`
}
type rateEntry struct {
	count int
	until time.Time
}

func randomText(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func digest(v string) []byte {
	d := sha256.Sum256([]byte(v))
	return d[:]
}

func normalizeUser(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

func validLabel(v string, limit int) bool {
	return v != "" && utf8.RuneCountInString(v) <= limit && strings.IndexFunc(v, unicode.IsControl) < 0
}

func passwordKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	enc := base64.RawStdEncoding
	return "argon2id-v1$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(passwordKey(password, salt))
}

func verifyPassword(password, hash string) bool {
	salt, want := make([]byte, 16), make([]byte, 32)
	valid := false
	parts := strings.Split(hash, "$")
	if len(parts) == 3 && parts[0] == "argon2id-v1" {
		a, e1 := base64.RawStdEncoding.DecodeString(parts[1])
		b, e2 := base64.RawStdEncoding.DecodeString(parts[2])
		if e1 == nil && e2 == nil && len(a) == 16 && len(b) == 32 {
			salt, want, valid = a, b, true
		}
	}
	return subtle.ConstantTimeCompare(passwordKey(password, salt), want) == 1 && valid
}

func (s *Server) limit(w http.ResponseWriter, key string, limit int) bool {
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if now.After(s.nextSweep) || len(s.attempts) >= 10000 {
		for k, e := range s.attempts {
			if !now.Before(e.until) {
				delete(s.attempts, k)
			}
		}
		s.nextSweep = now.Add(time.Minute)
	}
	e, exists := s.attempts[key]
	if !now.Before(e.until) {
		e = rateEntry{until: now.Add(time.Minute)}
	}
	if e.count >= limit || (!exists && len(s.attempts) >= 10000) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "操作太频繁，请稍后再试")
		return false
	}
	e.count++
	s.attempts[key] = e
	return true
}

func (s *Server) clientHost(r *http.Request) string {
	remote, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer := remote.Addr().WithZone("").Unmap()
	if peer == s.proxyIP {
		// The immediate proxy must overwrite X-Forwarded-For with one client IP.
		if values := r.Header.Values("X-Forwarded-For"); len(values) == 1 {
			client, err := netip.ParseAddr(strings.TrimSpace(values[0]))
			if err == nil && client.Zone() == "" {
				return client.Unmap().String()
			}
		}
	}
	return peer.String()
}

func (s *Server) acquirePassword(w http.ResponseWriter) bool {
	select {
	case s.gate <- struct{}{}:
		return true
	default:
		fail(w, 503, "登录繁忙，请稍后重试")
		return false
	}
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Registration == RegistrationOff {
		fail(w, 403, "此服务器未开放新账号注册")
		return
	}
	if !s.limit(w, "register:"+s.clientHost(r), 12) {
		return
	}
	var p registrationRequest
	if !readJSON(w, r, &p) {
		return
	}
	if s.cfg.Registration == RegistrationInvite && subtle.ConstantTimeCompare(digest(p.InviteCode), s.inviteHash) != 1 {
		fail(w, 403, "请输入有效的邀请码")
		return
	}
	p.Username = normalizeUser(p.Username)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if p.DisplayName == "" {
		p.DisplayName = p.Username
	}
	if !usernamePattern.MatchString(p.Username) || strings.HasPrefix(p.Username, "radish-") {
		fail(w, 400, "用户名需为 3–32 个英文字母、数字或 ._-，以字母或数字开头，不能以 radish- 开头")
		return
	}
	if !validLabel(p.DisplayName, 48) || utf8.RuneCountInString(p.Password) < 10 || len(p.Password) > 256 {
		fail(w, 400, "昵称最多 48 字，密码至少 10 个字符且不超过 256 字节")
		return
	}
	if !s.acquirePassword(w) {
		return
	}
	hash := hashPassword(p.Password)
	<-s.gate
	u := user{Username: p.Username, DisplayName: p.DisplayName}
	token := randomText(32)
	err := transaction(r.Context(), s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(r.Context(), "INSERT INTO users(username, display_name, password) VALUES (?,?,?)", p.Username, p.DisplayName, hash)
		if err != nil {
			return err
		}
		u.ID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return insertSession(r.Context(), tx, u.ID, token)
	})
	if recordConflict(err) {
		fail(w, 409, "用户名已被使用")
		return
	}
	if err != nil {
		internal(w, "register", err)
		return
	}
	s.setSession(w, token)
	reply(w, 201, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, "login:"+s.clientHost(r), 35) {
		return
	}
	var p credentials
	if !readJSON(w, r, &p) {
		return
	}
	key := normalizeUser(p.Username)
	if !usernamePattern.MatchString(key) || len(p.Password) > 256 {
		fail(w, 401, "用户名或密码不正确")
		return
	}
	if !s.limit(w, "login-user:"+key, 12) || !s.acquirePassword(w) {
		return
	}
	var u user
	var hash sql.NullString
	err := s.db.QueryRowContext(r.Context(), "SELECT id, username, display_name, password FROM users WHERE username=?", key).Scan(&u.ID, &u.Username, &u.DisplayName, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		<-s.gate
		internal(w, "login", err)
		return
	}
	valid := verifyPassword(p.Password, hash.String)
	<-s.gate
	if !valid || err != nil {
		fail(w, 401, "用户名或密码不正确")
		return
	}
	token := randomText(32)
	err = transaction(r.Context(), s.db, func(tx *sql.Tx) error {
		if old := s.sessionToken(r); old != "" {
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(old)); err != nil {
				return err
			}
		}
		return insertSession(r.Context(), tx, u.ID, token)
	})
	if err != nil {
		internal(w, "login-session", err)
		return
	}
	s.setSession(w, token)
	reply(w, 200, u)
}

func insertSession(ctx context.Context, tx *sql.Tx, id int64, token string) error {
	now := time.Now()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires <= ?", now.Unix()); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO sessions VALUES (?,?,?)", digest(token), id, now.Add(sessionAge).Unix())
	return err
}

func cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 43 {
		return ""
	}
	return c.Value
}

func (s *Server) setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	if s.secure && path == "/" {
		name = "__Host-" + name
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) setSession(w http.ResponseWriter, token string) {
	s.setCookie(w, sessionCookie, token, "/", int(sessionAge.Seconds()))
}

func (s *Server) sessionToken(r *http.Request) string {
	name := sessionCookie
	if s.secure {
		name = "__Host-" + name
	}
	return cookie(r, name)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token := s.sessionToken(r); token != "" {
		if _, err := s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(token)); err != nil {
			internal(w, "logout", err)
			return
		}
	}
	s.setCookie(w, sessionCookie, "", "/", -1)
	w.WriteHeader(204)
}

func (s *Server) identity(ctx context.Context, token string) (user, error) {
	var u user
	if token == "" {
		return u, sql.ErrNoRows
	}
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.display_name FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires>?`, digest(token), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.DisplayName)
	return u, err
}

func (s *Server) withUser(next func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.identity(r.Context(), s.sessionToken(r))
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 401, "请先登录")
			return
		}
		if err != nil {
			internal(w, "session", err)
			return
		}
		next(w, r, u)
	}
}
