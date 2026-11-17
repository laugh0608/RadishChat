package chat

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcBrowser = "radishchat_oidc"
const oidcPath = "/api/v1/auth/radish"

type oidcLogin struct {
	cfg    Config
	client *http.Client
	base   *url.URL
	style  oauth2.AuthStyle
}

func newOIDC(cfg Config, secure bool) (*oidcLogin, error) {
	if cfg.OIDCIssuer == "" && cfg.OIDCClientID == "" && cfg.OIDCSecret == "" && cfg.OIDCClientAuth == "" {
		return nil, nil
	}
	u, err := url.Parse(cfg.OIDCIssuer)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery ||
		!safeURL(u) || (secure && u.Scheme != "https") || cfg.OIDCClientID == "" || cfg.OIDCSecret == "" {
		return nil, errors.New("请同时配置安全的 OIDC issuer、client ID 和客户端密钥")
	}
	client := &http.Client{Timeout: 8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	style := oauth2.AuthStyleInHeader
	switch cfg.OIDCClientAuth {
	case "", "client_secret_basic":
	case "client_secret_post":
		style = oauth2.AuthStyleInParams
	default:
		return nil, errors.New("OIDC 客户端认证方式仅支持 client_secret_basic 或 client_secret_post")
	}
	return &oidcLogin{cfg, client, u, style}, nil
}

func (o *oidcLogin) discovery(ctx context.Context) (context.Context, *oidc.Provider, oauth2.Config, error) {
	ctx = oidc.ClientContext(ctx, o.client)
	var c oauth2.Config
	p, err := oidc.NewProvider(ctx, o.cfg.OIDCIssuer)
	if err != nil {
		return ctx, nil, c, err
	}
	var extra struct {
		JWKS string `json:"jwks_uri"`
	}
	if err := p.Claims(&extra); err != nil {
		return ctx, nil, c, err
	}
	e := p.Endpoint()
	for _, target := range []string{e.AuthURL, e.TokenURL, extra.JWKS} {
		u, err := url.Parse(target)
		if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" || !safeURL(u) || u.Scheme != o.base.Scheme {
			return ctx, nil, c, errors.New("OIDC 端点不满足当前协议要求")
		}
	}
	e.AuthStyle = o.style
	c = oauth2.Config{ClientID: o.cfg.OIDCClientID, ClientSecret: o.cfg.OIDCSecret, Endpoint: e,
		RedirectURL: o.cfg.PublicURL + oidcPath + "/callback", Scopes: []string{oidc.ScopeOpenID, "profile"}}
	return ctx, p, c, nil
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		fail(w, 404, "此服务器未配置 Radish 登录")
		return
	}
	if !s.limit(w, "oidc:"+s.clientHost(r), 50) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, _, c, err := s.oidc.discovery(ctx)
	if err != nil {
		fail(w, 502, "Radish 登录暂不可用")
		return
	}

	browser := cookie(r, oidcBrowser)
	if browser == "" {
		browser = randomText(32)
	}
	state, nonce, verifier := randomText(32), randomText(32), oauth2.GenerateVerifier()
	err = transaction(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM oidc_flows WHERE expires<=? OR browser_hash=?", time.Now().Unix(), digest(browser)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO oidc_flows VALUES (?,?,?,?,?)", state, digest(browser), nonce, verifier, time.Now().Add(5*time.Minute).Unix())
		return err
	})
	if err != nil {
		internal(w, "oidc-start", err)
		return
	}
	s.setCookie(w, oidcBrowser, browser, oidcPath, 300)
	reply(w, 200, map[string]string{"authorizationUrl": c.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))})
}

func oidcResult(w http.ResponseWriter, r *http.Request, key string) {
	http.Redirect(w, r, "/?login="+key, http.StatusSeeOther)
}

var errRegistrationClosed = errors.New("registration closed")

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		fail(w, 404, "此服务器未配置 Radish 登录")
		return
	}
	if !s.limit(w, "oidc-callback:"+s.clientHost(r), 80) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	q := r.URL.Query()
	browser, state := cookie(r, oidcBrowser), q.Get("state")
	if browser == "" || len(state) != 43 {
		oidcResult(w, r, "invalid")
		return
	}
	var nonce, verifier string
	err := s.db.QueryRowContext(ctx, `DELETE FROM oidc_flows WHERE state=? AND browser_hash=? AND expires>?
		RETURNING nonce,verifier`, state, digest(browser), time.Now().Unix()).Scan(&nonce, &verifier)
	if err != nil {
		oidcResult(w, r, "invalid")
		return
	}
	s.setCookie(w, oidcBrowser, "", oidcPath, -1)
	if q.Get("error") != "" || q.Get("code") == "" {
		oidcResult(w, r, "cancelled")
		return
	}
	ctx, p, c, err := s.oidc.discovery(ctx)
	if err != nil {
		oidcResult(w, r, "unavailable")
		return
	}
	tokens, err := c.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		oidcResult(w, r, "invalid")
		return
	}
	raw, _ := tokens.Extra("id_token").(string)
	id, err := p.Verifier(&oidc.Config{ClientID: c.ClientID}).Verify(ctx, raw)
	if err != nil || id.Subject == "" || len(id.Subject) > 255 ||
		subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(nonce)) != 1 {
		oidcResult(w, r, "invalid")
		return
	}
	var claims struct {
		Name            string `json:"name"`
		Preferred       string `json:"preferred_username"`
		AuthorizedParty string `json:"azp"`
	}
	if err := id.Claims(&claims); err != nil ||
		((len(id.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != c.ClientID) {
		oidcResult(w, r, "invalid")
		return
	}

	token := randomText(32)
	err = transaction(ctx, s.db, func(tx *sql.Tx) error {
		var userID int64
		err := tx.QueryRowContext(ctx, "SELECT user_id FROM external_accounts WHERE issuer=? AND subject=?", id.Issuer, id.Subject).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			if s.cfg.Registration != RegistrationOpen {
				return errRegistrationClosed
			}
			// 保留前缀，避免把同名的本地账号自动合并。
			username := "radish-" + strings.ToLower(randomText(12))
			name := strings.TrimSpace(claims.Name)
			if !validLabel(name, 48) {
				name = strings.TrimSpace(claims.Preferred)
			}
			if !validLabel(name, 48) {
				name = "Radish 用户"
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO users(username,display_name) VALUES (?,?)", username, name)
			if err != nil {
				return err
			}
			userID, err = res.LastInsertId()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO external_accounts VALUES (?,?,?)", id.Issuer, id.Subject, userID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if old := s.sessionToken(r); old != "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", digest(old)); err != nil {
				return err
			}
		}
		return insertSession(ctx, tx, userID, token, nil)
	})
	if errors.Is(err, errRegistrationClosed) {
		oidcResult(w, r, "closed")
		return
	}
	if errors.Is(err, errAccountUnavailable) {
		oidcResult(w, r, "invalid")
		return
	}
	if err != nil {
		internal(w, "oidc-account", err)
		return
	}
	s.setSession(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
