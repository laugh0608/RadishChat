package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	instance := flag.String("instance", "", "实例来源 URL，例如 https://chat.example.com")
	room := flag.Int64("room", 0, "群 ID")
	installation := flag.String("installation", "", "生效的安装 ID")
	stateDir := flag.String("state-dir", "", "Bot 专用的游标目录")
	ca := flag.String("ca-file", "", "可选，私有 CA 的 PEM 文件")
	resume := flag.String("resume-after", "", "人工确认跳过缺口时，填入服务端报告的 resumeAfter")
	flag.Parse()
	if flag.NArg() != 0 || *room <= 0 || !headerValue(*installation) || *stateDir == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *instance, *room, *installation, *stateDir, *ca, *resume); err != nil && ctx.Err() == nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, instance string, room int64, installation, stateDir, caFile, resume string) error {
	origin, err := instanceOrigin(instance)
	if err != nil {
		return err
	}
	token := strings.TrimRight(os.Getenv("RADISHCHAT_BOT_TOKEN"), "\r\n")
	if path := os.Getenv("RADISHCHAT_BOT_TOKEN_FILE"); path != "" {
		if token != "" {
			return errors.New("只能设置一种 Bot 凭据来源")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 Bot 凭据文件: %w", err)
		}
		token = strings.TrimRight(string(data), "\r\n")
	}
	if !headerValue(token) {
		return errors.New("请通过 RADISHCHAT_BOT_TOKEN_FILE 或 RADISHCHAT_BOT_TOKEN 提供有效凭据")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return fmt.Errorf("读取 CA 文件: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("加载系统 CA: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("CA 文件没有可读取的 PEM 证书")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("创建游标目录: %w", err)
	}
	scope := sha256.Sum256([]byte(origin + "\n" + installation))
	cursorPath := filepath.Join(stateDir, fmt.Sprintf("%x.cursor", scope))
	data, err := os.ReadFile(cursorPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取游标: %w", err)
	}
	if err == nil && !headerValue(string(data)) {
		return errors.New("保存的游标为空或格式无效，请检查游标文件")
	}

	bot := pingBot{client: client, eventsURL: fmt.Sprintf("%s/api/v1/bot/rooms/%d/events", origin, room), token: token, installation: installation, cursorPath: cursorPath}
	return bot.poll(ctx, string(data), resume)
}

func headerValue(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r > '~' })
}

func instanceOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" ||
		strings.Contains(raw, "#") || (u.EscapedPath() != "" && u.EscapedPath() != "/") {
		return "", errors.New("实例地址须为完整来源 URL，包含协议、主机及可选端口")
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if u.Scheme != "https" && (u.Scheme != "http" || (host != "localhost" && !net.ParseIP(host).IsLoopback())) {
		return "", errors.New("实例须使用 HTTPS，本机回环测试可使用 HTTP")
	}
	if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func saveCursor(path, cursor string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(cursor); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
