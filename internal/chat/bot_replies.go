package chat

import (
	"database/sql"
	"errors"
	"net/http"
	"time"
)

const botReplyRoute = "POST /api/v1/bot/rooms/{room}/events/{event}/reply"

var errBotReplyExpired = errors.New("Bot reply event expired")

func (s *Server) replyBotEvent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Markdown string `json:"markdown"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !validMessageMarkdown(input.Markdown) {
		fail(w, http.StatusBadRequest, "请输入最多 8000 字的消息")
		return
	}

	ctx := r.Context()
	eventID := r.PathValue("event")
	msg := message{Markdown: input.Markdown}
	var people []int64
	status := http.StatusOK
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		grant, err := botEventGrant(r, tx, botCommandsReply)
		if err != nil {
			return err
		}
		sequence, valid := parseBotEventCursor(eventID, grant.InstallationID)
		if !valid || sequence == 0 {
			return errMissing
		}
		last, floor, err := botEventWindow(ctx, tx, grant.InstallationID)
		if err != nil {
			return err
		}
		if sequence > last {
			return errMissing
		}
		if sequence <= floor {
			return errBotReplyExpired
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM bot_events
			WHERE installation_id=? AND sequence=? AND kind='command.requested'`, grant.InstallationID, sequence).Scan(&exists); err != nil {
			return err
		}

		msg.BotAuthor = &botMessageAuthor{Kind: "bot", ID: grant.BotID, DisplayName: grant.DisplayName}
		var previous string
		err = tx.QueryRowContext(ctx, `SELECT id,body,sent_at FROM messages
			WHERE room_id=? AND sender_bot_id=? AND request_id=?`,
			grant.RoomID, grant.BotID, eventID).Scan(&msg.ID, &previous, &msg.SentAt)
		if err == nil {
			if previous != msg.Markdown {
				return errRequestConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		msg.SentAt = time.Now().UnixMilli()
		if err := tx.QueryRowContext(ctx, `INSERT INTO messages(room_id,sender_bot_id,body,sent_at,request_id)
			VALUES (?,?,?,?,?) RETURNING id`, grant.RoomID, grant.BotID, msg.Markdown, msg.SentAt, eventID).Scan(&msg.ID); err != nil {
			return err
		}
		people, err = audience(ctx, tx, grant.RoomID)
		status = http.StatusCreated
		return err
	})
	switch {
	case errors.Is(err, errBotReplyExpired):
		fail(w, http.StatusGone, "命令事件已超过保留窗口")
	case errors.Is(err, errRequestConflict):
		fail(w, http.StatusConflict, "此事件已有不同正文的回复")
	case err != nil:
		botError(w, "reply-bot-event", err)
	default:
		s.events.notify(people...)
		reply(w, status, msg)
	}
}
