package chat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	botEventRetention     = 7 * 24 * time.Hour
	botEventCapacity      = 1000
	botEventDefaultPage   = 25
	botEventMaxPage       = 50
	botEventPollLimit     = 120 // Per Bot and installation per minute.
	botCommandSubmitLimit = 60  // Per account and installation per minute.
)

var (
	errBotEventRate  = errors.New("Bot event rate limit")
	errBotEventQuery = errors.New("invalid Bot event query")
	errBotCommand    = errors.New("unsupported Bot command")
)

type botCommandAccepted struct {
	EventID string `json:"eventId"`
}

type botEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Version    int    `json:"version"`
	OccurredAt int64  `json:"occurredAt"`
	Payload    struct {
		Command string `json:"command"`
	} `json:"payload"`
}

type botEventPage struct {
	Events     []botEvent `json:"events"`
	NextCursor string     `json:"nextCursor"`
	HasMore    bool       `json:"hasMore"`
}

func botEventCursor(installationID string, sequence int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(installationID + ":" + strconv.FormatInt(sequence, 10)))
}

func parseBotEventQuery(rawQuery, installationID string) (after int64, limit int, err error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil || len(values["after"]) > 1 || len(values["limit"]) > 1 {
		return 0, 0, errBotEventQuery
	}
	limit = botEventDefaultPage
	if values.Has("limit") {
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > botEventMaxPage {
			return 0, 0, errBotEventQuery
		}
	}
	if !values.Has("after") {
		return 0, limit, nil
	}
	raw := values.Get("after")
	if len(raw) > 80 {
		return 0, 0, errBotEventQuery
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return 0, 0, errBotEventQuery
	}
	installation, sequence, ok := strings.Cut(string(decoded), ":")
	if !ok || installation != installationID {
		return 0, 0, errBotEventQuery
	}
	after, err = strconv.ParseInt(sequence, 10, 64)
	if err != nil || after < 0 || botEventCursor(installationID, after) != raw {
		return 0, 0, errBotEventQuery
	}
	return after, limit, nil
}

func (s *Server) invokeBotCommand(w http.ResponseWriter, r *http.Request, u user) {
	var input struct {
		Command   string `json:"command"`
		RequestID string `json:"requestId"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !validLabel(input.Command, 32) || len(input.RequestID) < 16 || len(input.RequestID) > 64 ||
		!validLabel(input.RequestID, 64) {
		fail(w, http.StatusBadRequest, "命令或请求标识无效")
		return
	}

	ctx := r.Context()
	roomID := intID(r.PathValue("room"))
	installationID := r.PathValue("installation")
	var sequence int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		member, err := botGroupMembership(r, tx, roomID, u.ID)
		if err != nil {
			return err
		}
		var activeID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM bot_installations
			WHERE id=? AND room_id=? AND state='active' AND commands_receive=1`, installationID, roomID).Scan(&activeID); err != nil {
			return err
		}

		now := time.Now()
		cutoff := now.Add(-botEventRetention).UnixMilli()
		requestHash, commandHash := digest(input.RequestID), digest(input.Command)
		var previousHash []byte
		err = tx.QueryRowContext(ctx, `SELECT event_sequence,command_hash FROM bot_command_requests
			WHERE installation_id=? AND user_id=? AND membership_id=? AND request_hash=? AND accepted_at>?`,
			installationID, u.ID, member.membershipID, requestHash, cutoff).Scan(&sequence, &previousHash)
		if err == nil {
			if !bytes.Equal(previousHash, commandHash) {
				return errRequestConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if input.Command != "ping" {
			return errBotCommand
		}
		key := fmt.Sprintf("bot-command:%d:%s", u.ID, installationID)
		if !s.takeRateSlot(key, botCommandSubmitLimit) {
			return errBotEventRate
		}

		var floor int64
		if err := tx.QueryRowContext(ctx, `UPDATE bot_installations
			SET last_event_sequence=last_event_sequence+1,
			    pruned_event_sequence=max(pruned_event_sequence,last_event_sequence+1-?)
			WHERE id=? RETURNING last_event_sequence,pruned_event_sequence`,
			botEventCapacity, installationID).Scan(&sequence, &floor); err != nil {
			return err
		}
		at := now.UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_events(installation_id,sequence,occurred_at,kind,version,command)
			VALUES (?,?,?,'command.requested',1,?)`, installationID, sequence, at, input.Command); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bot_command_requests
			(installation_id,user_id,membership_id,request_hash,command_hash,event_sequence,accepted_at)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(installation_id,user_id,membership_id,request_hash) DO UPDATE SET
			    command_hash=excluded.command_hash,event_sequence=excluded.event_sequence,accepted_at=excluded.accepted_at`,
			installationID, u.ID, member.membershipID, requestHash, commandHash, sequence, at); err != nil {
			return err
		}
		if floor == 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM bot_events WHERE installation_id=? AND sequence<=?", installationID, floor)
		return err
	})
	if err != nil {
		botEventError(w, "invoke-bot-command", err)
		return
	}
	reply(w, http.StatusAccepted, botCommandAccepted{EventID: botEventCursor(installationID, sequence)})
}

func (s *Server) listBotEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var resumeAfter string
	page := botEventPage{Events: []botEvent{}}
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		grant, err := botInstallationForToken(r, tx)
		if err != nil {
			return err
		}
		headers := r.Header.Values(botInstallationHeader)
		if len(headers) != 1 || headers[0] != grant.InstallationID || !slices.Contains(grant.Permissions, botCommandsReceive) {
			return errMissing
		}
		key := "bot-poll:" + grant.BotID + ":" + grant.InstallationID
		if !s.takeRateSlot(key, botEventPollLimit) {
			return errBotEventRate
		}
		after, limit, err := parseBotEventQuery(r.URL.RawQuery, grant.InstallationID)
		if err != nil {
			return err
		}
		// Check the expiry boundary even before the next physical cleanup.
		cutoff := time.Now().Add(-botEventRetention).UnixMilli()
		var last, floor int64
		if err := tx.QueryRowContext(ctx, `SELECT i.last_event_sequence,max(i.pruned_event_sequence,coalesce((
			SELECT max(sequence) FROM bot_events WHERE installation_id=i.id AND occurred_at<=?
		),0)) FROM bot_installations i WHERE i.id=?`, cutoff, grant.InstallationID).Scan(&last, &floor); err != nil {
			return err
		}
		if after > last {
			return errBotEventQuery
		}
		if after < floor {
			resumeAfter = botEventCursor(grant.InstallationID, floor)
			return nil
		}

		return readBotEventPage(ctx, tx, grant.InstallationID, after, limit, &page)
	})
	if err != nil {
		botEventError(w, "list-bot-events", err)
		return
	}
	if resumeAfter != "" {
		reply(w, http.StatusGone, map[string]string{"error": "部分事件已超过保留窗口", "resumeAfter": resumeAfter})
		return
	}
	reply(w, http.StatusOK, page)
}

func readBotEventPage(ctx context.Context, tx *sql.Tx, installationID string, after int64, limit int, page *botEventPage) error {
	rows, err := tx.QueryContext(ctx, `SELECT sequence,occurred_at,kind,version,command
		FROM bot_events WHERE installation_id=? AND sequence>? ORDER BY sequence LIMIT ?`,
		installationID, after, limit+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		var item botEvent
		if err := rows.Scan(&after, &item.OccurredAt, &item.Type, &item.Version, &item.Payload.Command); err != nil {
			return err
		}
		item.ID = botEventCursor(installationID, after)
		page.Events = append(page.Events, item)
	}
	page.NextCursor = botEventCursor(installationID, after)
	return rows.Err()
}

func botEventError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, errBotEventRate):
		rateLimited(w)
	case errors.Is(err, errBotEventQuery):
		fail(w, http.StatusBadRequest, "事件游标或分页参数无效")
	case errors.Is(err, errBotCommand):
		fail(w, http.StatusBadRequest, "不支持此 Bot 命令")
	case errors.Is(err, errRequestConflict):
		fail(w, http.StatusConflict, "请求标识已用于其他命令")
	default:
		botError(w, operation, err)
	}
}
