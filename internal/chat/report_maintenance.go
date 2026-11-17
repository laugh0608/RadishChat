package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	ReportDismissed       = "dismissed"
	ReportAccountDisabled = "account_disabled"
	ReportReferred        = "referred_to_group_owner"
	closedReportRetention = 30 * 24 * time.Hour
)

type MessageReport struct {
	ID             int64  `json:"id"`
	RoomID         int64  `json:"roomId"`
	MessageID      int64  `json:"messageId,omitempty"`
	ReporterUserID int64  `json:"reporterUserId"`
	Reason         string `json:"reason"`
	CreatedAt      int64  `json:"createdAt"`
	Status         string `json:"status"`
	Decision       string `json:"decision,omitempty"`
	ClosedAt       int64  `json:"closedAt,omitempty"`
}

type ReportEvidence struct {
	AuthorUserID int64  `json:"authorUserId,omitempty"`
	AuthorBotID  string `json:"authorBotId,omitempty"`
	Markdown     string `json:"markdown"`
}

type MessageReportReview struct {
	Report   MessageReport   `json:"report"`
	Evidence *ReportEvidence `json:"evidence"`
}

const reportStatus = `CASE WHEN decision IS NULL THEN 'open' ELSE 'closed' END`
const reportColumns = `id,room_id,coalesce(message_id,0),reporter_user_id,reason,created_at,` + reportStatus + `,coalesce(decision,''),coalesce(closed_at,0)`

func scanMessageReport(row interface{ Scan(...any) error }, report *MessageReport) error {
	return row.Scan(&report.ID, &report.RoomID, &report.MessageID, &report.ReporterUserID, &report.Reason, &report.CreatedAt,
		&report.Status, &report.Decision, &report.ClosedAt)
}

// ListMessageReports reads case metadata without retrieving message bodies.
func ListMessageReports(ctx context.Context, database, status string, before int64, limit int) ([]MessageReport, error) {
	if status != "" && status != "open" && status != "closed" {
		return nil, errors.New("举报状态须为 open 或 closed")
	}
	if before < 0 || limit < 1 || limit > 200 {
		return nil, errors.New("举报游标须为非负整数，页大小须为 1–200")
	}
	db, err := openMaintenanceDB(ctx, database, "ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT `+reportColumns+` FROM message_reports
		WHERE (closed_at IS NULL OR closed_at>?) AND (?='' OR `+reportStatus+`=?) AND (?=0 OR id<?)
		ORDER BY id DESC LIMIT ?`, time.Now().Add(-closedReportRetention).UnixMilli(), status, status, before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("查询举报: %w", err)
	}
	defer rows.Close()
	reports := make([]MessageReport, 0, limit)
	for rows.Next() {
		var report MessageReport
		if err := scanMessageReport(rows, &report); err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

func readMessageReport(ctx context.Context, tx *sql.Tx, id, replayClosedAt int64) (MessageReport, error) {
	var report MessageReport
	err := scanMessageReport(tx.QueryRowContext(ctx, `SELECT `+reportColumns+` FROM message_reports
		WHERE id=? AND (closed_at IS NULL OR closed_at>? OR closed_at=nullif(?,0))`,
		id, time.Now().Add(-closedReportRetention).UnixMilli(), replayClosedAt), &report)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageReport{}, fmt.Errorf("举报 %d 不存在或已超过留存期", id)
	}
	return report, err
}

// ReviewMessageReport records access before making the selected message available to the operator.
func ReviewMessageReport(ctx context.Context, database string, id int64) (MessageReportReview, error) {
	var review MessageReportReview
	db, err := openMaintenanceDB(ctx, database, "rw")
	if err != nil {
		return review, err
	}
	defer db.Close()
	err = transaction(ctx, db, func(tx *sql.Tx) error {
		report, err := readMessageReport(ctx, tx, id, 0)
		if err != nil {
			return err
		}
		review.Report = report
		var evidence ReportEvidence
		err = tx.QueryRowContext(ctx, `SELECT coalesce(sender_id,0),coalesce(sender_bot_id,''),body
			FROM messages WHERE id=? AND room_id=?`, report.MessageID, report.RoomID).
			Scan(&evidence.AuthorUserID, &evidence.AuthorBotID, &evidence.Markdown)
		if errors.Is(err, sql.ErrNoRows) {
			return appendMaintenanceAudit(ctx, tx, auditReportReview, 0, auditEvidenceMissing, id)
		}
		if err != nil {
			return err
		}
		review.Evidence = &evidence
		return appendMaintenanceAudit(ctx, tx, auditReportReview, evidence.AuthorUserID, auditSuccess, id)
	})
	if err != nil {
		return MessageReportReview{}, err
	}
	return review, nil
}

// CloseMessageReport commits its decision, any account suspension, and both audit entries together.
func CloseMessageReport(ctx context.Context, database string, id int64, decision string) (MessageReport, error) {
	return closeMessageReport(ctx, database, id, decision, 0)
}

// ReplayMessageReportClosure reapplies a verified decision to a restored snapshot.
func ReplayMessageReportClosure(ctx context.Context, database string, id int64, decision string, closedAt int64) (MessageReport, error) {
	if closedAt <= 0 {
		return MessageReport{}, errors.New("原结案时间须为正整数 Unix 毫秒")
	}
	return closeMessageReport(ctx, database, id, decision, closedAt)
}

func closeMessageReport(ctx context.Context, database string, id int64, decision string, originalClosedAt int64) (MessageReport, error) {
	switch decision {
	case ReportDismissed, ReportAccountDisabled, ReportReferred:
	default:
		return MessageReport{}, errors.New("结案结果须为 dismissed、account_disabled 或 referred_to_group_owner")
	}
	db, err := openMaintenanceDB(ctx, database, "rw")
	if err != nil {
		return MessageReport{}, err
	}
	defer db.Close()
	var report MessageReport
	err = transaction(ctx, db, func(tx *sql.Tx) error {
		closedAt := time.Now().UnixMilli()
		if originalClosedAt != 0 {
			if originalClosedAt > closedAt {
				return errors.New("原结案时间晚于当前时间")
			}
			closedAt = originalClosedAt
		}
		// Take the write lock before reading the report so competing operators see the committed decision.
		result, err := tx.ExecContext(ctx, `UPDATE message_reports SET decision=?,closed_at=? WHERE id=? AND decision IS NULL`,
			decision, closedAt, id)
		if err != nil {
			return fmt.Errorf("结案举报: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		report, err = readMessageReport(ctx, tx, id, originalClosedAt)
		if err != nil {
			return err
		}
		if count == 0 {
			if report.Decision == decision {
				if originalClosedAt != 0 && report.ClosedAt != originalClosedAt {
					return errors.New("原结案时间与已有结案记录不一致")
				}
				return nil
			}
			return fmt.Errorf("举报 %d 无法改为所选结果，当前状态 %s，处理结果 %q", id, report.Status, report.Decision)
		}
		if originalClosedAt != 0 && originalClosedAt < report.CreatedAt {
			return errors.New("原结案时间早于举报创建时间")
		}

		var authorUserID int64
		var authorBotID string
		if decision != ReportDismissed {
			err := tx.QueryRowContext(ctx, `SELECT coalesce(sender_id,0),coalesce(sender_bot_id,'') FROM messages WHERE id=? AND room_id=?`,
				report.MessageID, report.RoomID).Scan(&authorUserID, &authorBotID)
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("举报原文不可用，无法按作者处置")
			}
			if err != nil {
				return err
			}
		}
		if decision == ReportAccountDisabled {
			if authorUserID == 0 {
				return errors.New("停用结案适用于本地账号作者")
			}
			if err := disableLocalAccount(ctx, tx, authorUserID, id); err != nil {
				return err
			}
		}
		if decision == ReportReferred && authorBotID == "" {
			return errors.New("转交群主结案适用于 Bot 消息")
		}
		return appendMaintenanceAudit(ctx, tx, auditReportClose, authorUserID, decision, id)
	})
	if err != nil {
		return MessageReport{}, err
	}
	return report, nil
}
