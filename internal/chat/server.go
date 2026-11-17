package chat

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	PublicURL, Database, WebDir                          string
	OIDCIssuer, OIDCClientID, OIDCSecret, OIDCClientAuth string
	Registration                                         RegistrationMode
	InviteCode, TrustedProxyIP                           string
}

type Server struct {
	db                  *sql.DB
	mux                 *http.ServeMux
	cfg                 Config
	oidc                *oidcLogin
	secure              bool
	proxyIP             netip.Addr
	inviteHash          []byte
	events              subscribers
	gate                chan struct{}
	stopBotEventCleanup func()
	rateMu              sync.Mutex
	attempts            map[string]rateEntry
	nextSweep           time.Time
}

func originURL(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.Path != "" || u.RawPath != "" {
		return nil, false
	}
	return u, safeURL(u)
}

func safeURL(u *url.URL) bool {
	host := u.Hostname()
	ip := net.ParseIP(host)
	return u.Scheme == "https" || (u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback())))
}

func New(cfg Config) (*Server, error) {
	cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/")
	u, ok := originURL(cfg.PublicURL)
	if !ok || cfg.PublicURL != u.Scheme+"://"+u.Host {
		return nil, errors.New("RADISHCHAT_PUBLIC_URL 必须为 HTTPS 源地址，本机回环地址可用 HTTP")
	}
	var inviteHash []byte
	switch cfg.Registration {
	case "":
		cfg.Registration = RegistrationOff
	case RegistrationOff, RegistrationOpen:
	case RegistrationInvite:
		if !inviteCodePattern.MatchString(cfg.InviteCode) {
			return nil, errors.New("邀请码模式需要 48 位小写十六进制 RADISHCHAT_INVITE_CODE")
		}
		inviteHash = digest(cfg.InviteCode)
	default:
		return nil, errors.New("RADISHCHAT_REGISTRATION 仅接受 off、on 或 invite")
	}
	cfg.InviteCode = ""
	var proxyIP netip.Addr
	if cfg.TrustedProxyIP != "" {
		var err error
		proxyIP, err = netip.ParseAddr(cfg.TrustedProxyIP)
		if err != nil || proxyIP.Zone() != "" {
			return nil, errors.New("RADISHCHAT_TRUSTED_PROXY_IP 必须为单个 IPv4 或 IPv6 地址")
		}
		proxyIP = proxyIP.Unmap()
	}
	o, err := newOIDC(cfg, u.Scheme == "https")
	if err != nil {
		return nil, err
	}
	db, err := openDB(cfg.Database)
	if err != nil {
		return nil, err
	}
	s := &Server{db: db, mux: http.NewServeMux(), cfg: cfg, oidc: o, secure: u.Scheme == "https", proxyIP: proxyIP,
		inviteHash: inviteHash, gate: make(chan struct{}, 4), attempts: make(map[string]rateEntry)}
	s.routes()
	s.stopBotEventCleanup = startBotEventCleanup(db)
	return s, nil
}

func (s *Server) Close() error {
	s.stopBotEventCleanup()
	return s.db.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			fail(w, 503, "数据库不可用")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{
			"name": "RadishChat", "apiVersion": 1, "registration": s.cfg.Registration != RegistrationOff,
			"registrationInviteRequired": s.cfg.Registration == RegistrationInvite,
			"radishLogin":                s.oidc != nil, "endToEndEncryption": false,
		})
	})
	s.mux.HandleFunc("POST /api/v1/register", s.register)
	s.mux.HandleFunc("POST /api/v1/login", s.login)
	s.mux.HandleFunc("POST /api/v1/logout", s.logout)
	s.mux.HandleFunc("POST /api/v1/auth/local/recover", s.recoverLocalAccount)
	s.mux.HandleFunc("POST /api/v1/auth/radish/start", s.oidcStart)
	s.mux.HandleFunc("GET /api/v1/auth/radish/callback", s.oidcCallback)
	s.mux.HandleFunc("GET /api/v1/me", s.withUser(func(w http.ResponseWriter, _ *http.Request, u user) { reply(w, 200, u) }))
	s.mux.HandleFunc("GET /api/v1/bots", s.withUser(s.listBots))
	s.mux.HandleFunc("POST /api/v1/bots", s.withUser(s.createBot))
	s.mux.HandleFunc("POST /api/v1/bots/{bot}/credential/rotate", s.withUser(s.rotateBotCredential))
	s.mux.HandleFunc("DELETE /api/v1/bots/{bot}/credential", s.withUser(s.revokeBotCredential))
	s.mux.HandleFunc("GET /api/v1/bots/{bot}/installations", s.withUser(s.listOwnedBotInstallations))
	s.mux.HandleFunc("POST /api/v1/bots/{bot}/installations/{installation}/approve", s.withUser(s.approveBotInstallation))
	s.mux.HandleFunc("DELETE /api/v1/bot-installations/{installation}", s.withUser(s.removeBotInstallation))
	s.mux.HandleFunc("GET /api/v1/bot/me", s.botMe)
	s.mux.HandleFunc("GET /api/v1/bot/rooms/{room}/installation", s.botRoomInstallation)
	s.mux.HandleFunc("GET /api/v1/bot/rooms/{room}/events", s.listBotEvents)
	s.mux.HandleFunc(botReplyRoute, s.replyBotEvent)
	s.mux.HandleFunc("GET /api/v1/events", s.withUser(s.stream))
	s.mux.HandleFunc("GET /api/v1/rooms", s.withUser(s.listRooms))
	s.mux.HandleFunc("POST /api/v1/rooms/direct", s.withUser(s.directRoom))
	s.mux.HandleFunc("POST /api/v1/rooms/group", s.withUser(s.groupRoom))
	s.mux.HandleFunc("GET /api/v1/rooms/{room}/bot-installations", s.withUser(s.listGroupBotInstallations))
	s.mux.HandleFunc("GET /api/v1/rooms/{room}/bots/{bot}/installation", s.withUser(s.groupBotInstallation))
	s.mux.HandleFunc("PUT /api/v1/rooms/{room}/bots/{bot}/installation", s.withUser(s.groupBotInstallation))
	s.mux.HandleFunc("POST /api/v1/rooms/{room}/bot-installations/{installation}/commands", s.withUser(s.invokeBotCommand))
	s.mux.HandleFunc("GET /api/v1/rooms/{room}/members", s.withUser(s.listMembers))
	s.mux.HandleFunc("POST /api/v1/rooms/{room}/members", s.withUser(s.addMember))
	s.mux.HandleFunc("DELETE /api/v1/rooms/{room}/members/{member}", s.withUser(s.removeMember))
	s.mux.HandleFunc("POST /api/v1/rooms/{room}/leave", s.withUser(s.leaveGroup))
	s.mux.HandleFunc("GET /api/v1/rooms/{room}/messages", s.withUser(s.listMessages))
	s.mux.HandleFunc("POST /api/v1/rooms/{room}/messages", s.withUser(s.sendMessage))
	s.mux.HandleFunc("POST /api/v1/rooms/{room}/messages/{message}/reports", s.withUser(s.submitMessageReport))
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { fail(w, 404, "接口不存在") })

	if s.cfg.WebDir != "" {
		assets := http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.cfg.WebDir, "assets"))))
		s.mux.Handle("GET /assets/", assets)
		s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
		})
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") != s.cfg.PublicURL {
				// The reply route uses explicit Bearer credentials, never a session cookie.
				if _, pattern := s.mux.Handler(r); pattern != botReplyRoute {
					fail(w, 403, "请求来源不匹配")
					return
				}
			}
		}
	}
	s.mux.ServeHTTP(w, r)
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}

func internal(w http.ResponseWriter, operation string, err error) {
	slog.Error("处理请求失败", "operation", operation, "type", fmt.Sprintf("%T", err))
	fail(w, 500, "服务器暂时无法完成请求")
}

func readJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	if ct := strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]); ct != "application/json" {
		fail(w, 415, "请求必须使用 application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil || dec.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求格式无效或过大")
		return false
	}
	return true
}

func intID(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0
	}
	return id
}
