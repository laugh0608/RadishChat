package chat

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var errReportReasonConflict = errors.New("different reason for existing report")

func (s *Server) submitMessageReport(w http.ResponseWriter, r *http.Request, u user) {
	var input struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	switch input.Reason {
	case "spam", "harassment", "other":
	default:
		fail(w, http.StatusBadRequest, "请选择有效的举报原因")
		return
	}
	if !s.limit(w, "report-user:"+fmt.Sprint(u.ID), 10) || !s.limit(w, "report-source:"+s.clientHost(r), 30) {
		return
	}
	ctx := r.Context()
	roomID, messageID := intID(r.PathValue("room")), intID(r.PathValue("message"))
	var id int64
	status := http.StatusOK
	err := s.userTransaction(r, u.ID, func(tx *sql.Tx) error {
		member, err := access(r, tx, roomID, u.ID)
		if err != nil {
			return err
		}
		if member.roomKind != "group" || messageID <= member.joinedAfterMessageID {
			return errMissing
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE id=? AND room_id=?)", messageID, roomID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errMissing
		}
		var reason string
		err = tx.QueryRowContext(ctx, "SELECT id,reason FROM message_reports WHERE reporter_user_id=? AND message_id=?", u.ID, messageID).Scan(&id, &reason)
		if err == nil {
			if reason != input.Reason {
				return errReportReasonConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		status = http.StatusCreated
		return tx.QueryRowContext(ctx, `INSERT INTO message_reports(room_id,message_id,reporter_user_id,reason,created_at)
			VALUES (?,?,?,?,?) RETURNING id`, roomID, messageID, u.ID, input.Reason, time.Now().UnixMilli()).Scan(&id)
	})
	switch {
	case errors.Is(err, errMissing):
		fail(w, http.StatusNotFound, "消息不存在或不可举报")
	case errors.Is(err, errReportReasonConflict):
		fail(w, http.StatusConflict, "已举报此消息，原举报原因不可更改")
	case err != nil:
		userError(w, "submit-message-report", err)
	default:
		reply(w, status, map[string]int64{"id": id})
	}
}
