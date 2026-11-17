package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"radishchat/internal/chat"
)

const defaultDatabase = "data/chat.sqlite"

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
	s, err := chat.New(chat.Config{
		PublicURL:      env("RADISHCHAT_PUBLIC_URL", "http://127.0.0.1:8080"),
		Database:       env("RADISHCHAT_DATABASE", defaultDatabase),
		WebDir:         env("RADISHCHAT_WEB_DIR", "web/dist"),
		OIDCIssuer:     os.Getenv("RADISHCHAT_OIDC_ISSUER"),
		OIDCClientID:   os.Getenv("RADISHCHAT_OIDC_CLIENT_ID"),
		OIDCClientAuth: os.Getenv("RADISHCHAT_OIDC_CLIENT_AUTH"),
		OIDCSecret:     secret,
		Registration:   chat.RegistrationMode(env("RADISHCHAT_REGISTRATION", "off")),
		InviteCode:     os.Getenv("RADISHCHAT_INVITE_CODE"),
		TrustedProxyIP: os.Getenv("RADISHCHAT_TRUSTED_PROXY_IP"),
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

func execute(args []string) error {
	if len(args) == 0 {
		return run()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	database := env("RADISHCHAT_DATABASE", defaultDatabase)
	switch {
	case len(args) == 2 && args[0] == "backup" && args[1] != "":
		if err := chat.CopyDatabase(ctx, database, args[1]); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	case len(args) == 2 && args[0] == "restore" && args[1] != "":
		if err := chat.CopyDatabase(ctx, args[1], database); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
	case len(args) == 3 && args[0] == "account":
		return executeAccount(ctx, database, args[1], args[2])
	case len(args) >= 2 && args[0] == "ops" && args[1] == "audit":
		return executeAudit(ctx, database, args[2:])
	case len(args) >= 2 && args[0] == "ops" && args[1] == "reports":
		return executeReports(ctx, database, args[2:])
	case len(args) >= 2 && args[0] == "ops" && args[1] == "data":
		return executeGovernanceData(ctx, database, args[2:])
	default:
		return errors.New("用法: radishchat [backup <备份文件> | restore <备份文件> | account <操作> <用户 ID> | ops audit | ops reports | ops data]")
	}
	return nil
}

func executeAccount(ctx context.Context, database, action, userID string) error {
	id, err := strconv.ParseInt(userID, 10, 64)
	if err != nil || id <= 0 {
		return errors.New("用户 ID 必须为正整数")
	}
	switch action {
	case "disable":
		if err := chat.DisableLocalAccount(ctx, database, id); err != nil {
			return fmt.Errorf("account disable: %w", err)
		}
		slog.Info("账号已停用", "user_id", id)
	case "issue-recovery", "reopen":
		code, err := chat.IssueLocalAccountRecovery(ctx, database, id, action == "reopen")
		if err != nil {
			return fmt.Errorf("account %s: %w", action, err)
		}
		if _, err := fmt.Fprintln(os.Stdout, code); err != nil {
			return fmt.Errorf("输出恢复码: %w", err)
		}
	default:
		return errors.New("用法: radishchat account <disable|issue-recovery|reopen> <用户 ID>")
	}
	return nil
}

func executeAudit(ctx context.Context, database string, args []string) error {
	flags := flag.NewFlagSet("ops audit", flag.ContinueOnError)
	before := flags.Int64("before", 0, "只查看编号小于此值的记录，0 表示从最新记录开始")
	limit := flags.Int("limit", 50, "返回记录数，范围 1–200")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("用法: radishchat ops audit [--before ID] [--limit N]")
	}
	entries, err := chat.ListMaintenanceAudit(ctx, database, *before, *limit)
	if err != nil {
		return fmt.Errorf("ops audit: %w", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(entries); err != nil {
		return fmt.Errorf("输出维护审计: %w", err)
	}
	return nil
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		slog.Error("RadishChat 执行失败", "error", err)
		os.Exit(1)
	}
}
