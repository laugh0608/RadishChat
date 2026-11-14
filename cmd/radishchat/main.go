package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"radishchat/internal/chat"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func run() error {
	secret := os.Getenv("RADISHCHAT_OIDC_CLIENT_SECRET")
	if path := os.Getenv("RADISHCHAT_OIDC_CLIENT_SECRET_FILE"); path != "" {
		if secret != "" {
			return errors.New("只能设置一种 OIDC 密钥来源")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return errors.New("无法读取 OIDC 密钥文件")
		}
		secret = strings.TrimRight(string(data), "\r\n")
	}
	enabled := env("RADISHCHAT_REGISTRATION", "on")
	if enabled != "on" && enabled != "off" {
		return errors.New("RADISHCHAT_REGISTRATION 仅接受 on 或 off")
	}
	s, err := chat.New(chat.Config{
		PublicURL:      env("RADISHCHAT_PUBLIC_URL", "http://127.0.0.1:8080"),
		Database:       env("RADISHCHAT_DATABASE", "data/chat.sqlite"),
		WebDir:         env("RADISHCHAT_WEB_DIR", "web/dist"),
		OIDCIssuer:     os.Getenv("RADISHCHAT_OIDC_ISSUER"),
		OIDCClientID:   os.Getenv("RADISHCHAT_OIDC_CLIENT_ID"),
		OIDCClientAuth: os.Getenv("RADISHCHAT_OIDC_CLIENT_AUTH"),
		OIDCSecret:     secret, DisableRegistration: enabled == "off",
	})
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpServer := &http.Server{Addr: env("RADISHCHAT_LISTEN", "127.0.0.1:8080"), Handler: s,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	slog.Info("RadishChat 已启动", "listen", listener.Addr())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		timeout, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(timeout); err != nil {
			_ = httpServer.Close()
		}
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		slog.Error("RadishChat 启动失败", "error", err)
		os.Exit(1)
	}
}
