package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type pingBot struct {
	client       *http.Client
	eventsURL    string
	token        string
	installation string
	cursorPath   string
}

type eventPage struct {
	Events []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Version int    `json:"version"`
		Payload struct {
			Command string `json:"command"`
		} `json:"payload"`
	} `json:"events"`
	NextCursor  string `json:"nextCursor"`
	HasMore     bool   `json:"hasMore"`
	ResumeAfter string `json:"resumeAfter"`
}

func (b pingBot) poll(ctx context.Context, cursor, resume string) error {
pollPage:
	for ctx.Err() == nil {
		endpoint := b.eventsURL
		if cursor != "" {
			endpoint += "?" + url.Values{"after": {cursor}}.Encode()
		}
		res, err := b.request(ctx, http.MethodGet, endpoint, "")
		if err != nil {
			return err
		}
		if res.status != http.StatusOK && res.status != http.StatusGone {
			return statusError("拉取命令", res.status)
		}
		var page eventPage
		if err := json.Unmarshal(res.body, &page); err != nil {
			return fmt.Errorf("读取命令页 JSON: %w", err)
		}
		if res.status == http.StatusGone {
			if !headerValue(page.ResumeAfter) || page.ResumeAfter == cursor {
				return errors.New("命令页 410 缺少有效的恢复游标")
			}
			if resume != page.ResumeAfter {
				return fmt.Errorf("部分命令已超过保留窗口。确认跳过后，重启并传入 -resume-after %q", page.ResumeAfter)
			}
			log.Printf("操作者已确认跳过缺口，继续读取 %q 之后的命令", page.ResumeAfter)
			if err := b.save(page.ResumeAfter); err != nil {
				return err
			}
			cursor, resume = page.ResumeAfter, ""
			continue
		}
		if page.Events == nil || !headerValue(page.NextCursor) || (page.HasMore && len(page.Events) == 0) {
			return errors.New("命令页缺少有效的事件或游标")
		}
		if len(page.Events) > 0 && page.NextCursor == cursor {
			return errors.New("命令页有事件，但游标未推进")
		}
		for _, event := range page.Events {
			if !headerValue(event.ID) || event.ID == "." || event.ID == ".." ||
				event.Type != "command.requested" || event.Version != 1 || event.Payload.Command != "ping" {
				return errors.New("命令页含无法处理的事件，游标未推进")
			}
			endpoint := b.eventsURL + "/" + url.PathEscape(event.ID) + "/reply"
			res, err := b.request(ctx, http.MethodPost, endpoint, `{"markdown":"pong"}`)
			if err != nil {
				return err
			}
			switch res.status {
			case http.StatusOK, http.StatusCreated:
			case http.StatusGone:
				// Re-read from the last saved position to discover the gap.
				continue pollPage
			default:
				return statusError("回复命令", res.status)
			}
		}
		if len(page.Events) > 0 {
			if err := b.save(page.NextCursor); err != nil {
				return err
			}
			cursor = page.NextCursor
		}
		if !page.HasMore && !wait(ctx, 5*time.Second) {
			break
		}
	}
	return ctx.Err()
}

func (b pingBot) save(cursor string) error {
	if err := saveCursor(b.cursorPath, cursor); err != nil {
		return fmt.Errorf("保存游标: %w", err)
	}
	return nil
}

func statusError(operation string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s返回 HTTP 401，凭据已失效，请更新凭据后重启", operation)
	case http.StatusNotFound:
		return fmt.Errorf("%s返回 HTTP 404，已停止此安装，请检查安装和权限", operation)
	default:
		return fmt.Errorf("%s返回 HTTP %d，游标未推进", operation, status)
	}
}

type response struct {
	status     int
	retryAfter string
	body       []byte
}

func (b pingBot) request(ctx context.Context, method, endpoint, body string) (response, error) {
	for backoff := time.Second; ctx.Err() == nil; backoff = min(backoff*2, 30*time.Second) {
		res, err := b.exchange(ctx, method, endpoint, body)
		if err == nil && res.status != http.StatusTooManyRequests && res.status < 500 {
			return res, nil
		}
		delay := backoff/2 + rand.N(backoff/2)
		if res.status == http.StatusTooManyRequests {
			delay = max(delay, retryDelay(res.retryAfter, time.Now()))
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			log.Printf("%s 请求失败，等待 %s 后重试: %v", method, delay.Round(time.Millisecond), err)
		} else {
			log.Printf("%s 返回 HTTP %d，等待 %s 后重试", method, res.status, delay.Round(time.Millisecond))
		}
		if !wait(ctx, delay) {
			break
		}
	}
	return response{}, ctx.Err()
}

func (b pingBot) exchange(ctx context.Context, method, endpoint, body string) (response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("X-RadishChat-Bot-Installation", b.installation)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()
	result := response{status: res.StatusCode, retryAfter: res.Header.Get("Retry-After")}
	if method == http.MethodGet && (result.status == http.StatusOK || result.status == http.StatusGone) {
		result.body, err = io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return result, err
	}
	// Reply status alone confirms the write. Read a bounded body to reuse the connection.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 32<<10))
	return result, nil
}

func retryDelay(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseUint(value, 10, 33); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, date.Sub(now))
	}
	return 0
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
