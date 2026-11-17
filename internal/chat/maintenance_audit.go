package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const maintenanceAuditRetention = 90 * 24 * time.Hour

const (
	auditAccountDisable  = "account.disable"
	auditIssueRecovery   = "account.issue-recovery"
	auditAccountReopen   = "account.reopen"
	auditReportReview    = "report.review"
	auditReportClose     = "report.close"
	auditDataExport      = "data.export"
	auditDataPrune       = "data.prune"
	auditSuccess         = "success"
	auditAlreadyDisabled = "already_disabled"
	auditEvidenceMissing = "evidence_unavailable"
)

type MaintenanceAuditEntry struct {
	ID           int64              `json:"id"`
	OccurredAt   int64              `json:"occurredAt"`
	Source       string             `json:"source"`
	Action       string             `json:"action"`
	TargetUserID int64              `json:"targetUserId,omitempty"`
	ReportID     int64              `json:"reportId,omitempty"`
	Result       string             `json:"result"`
	Cleanup      *GovernanceCleanup `json:"cleanup,omitempty"`
}

func openMaintenanceDB(ctx context.Context, database, mode string) (*sql.DB, error) {
	path, err := filepath.Abs(database)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径: %w", err)
	}
	db, err := sqliteDatabase(path, mode)
	if err != nil {
		return nil, fmt.Errorf("打开现有数据库: %w", err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("读取现有数据库版本: %w", err)
	}
	if version != currentSchemaVersion {
		db.Close()
		return nil, fmt.Errorf("数据库版本为 v%d，维护命令要求 v%d", version, currentSchemaVersion)
	}
	return db, nil
}

func appendMaintenanceAudit(ctx context.Context, tx *sql.Tx, action string, targetUserID int64, result string, reportID int64) error {
	return writeMaintenanceAudit(ctx, tx, MaintenanceAuditEntry{
		OccurredAt: time.Now().UnixMilli(), Action: action, TargetUserID: targetUserID, Result: result, ReportID: reportID,
	})
}

func writeMaintenanceAudit(ctx context.Context, tx *sql.Tx, entry MaintenanceAuditEntry) error {
	var reportsDeleted, auditDeleted any
	if entry.Cleanup != nil {
		reportsDeleted, auditDeleted = entry.Cleanup.ReportsDeleted, entry.Cleanup.AuditDeleted
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO maintenance_audit(occurred_at,source,action,target_user_id,result,report_id,reports_deleted,audit_deleted)
		VALUES (?,'maintenance_cli',?,nullif(?,0),?,nullif(?,0),?,?) RETURNING id`,
		entry.OccurredAt, entry.Action, entry.TargetUserID, entry.Result, entry.ReportID, reportsDeleted, auditDeleted).Scan(&id)
	if err != nil {
		return fmt.Errorf("写入维护审计: %w", err)
	}
	return nil
}

// ListMaintenanceAudit returns the newest rows in the retention window. A zero before starts at the newest row.
func ListMaintenanceAudit(ctx context.Context, database string, before int64, limit int) ([]MaintenanceAuditEntry, error) {
	if before < 0 || limit < 1 || limit > 200 {
		return nil, errors.New("审计游标须为非负整数，页大小须为 1–200")
	}
	db, err := openMaintenanceDB(ctx, database, "ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,occurred_at,source,action,coalesce(target_user_id,0),result,coalesce(report_id,0),
		reports_deleted,audit_deleted FROM maintenance_audit
		WHERE occurred_at>? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`,
		time.Now().Add(-maintenanceAuditRetention).UnixMilli(), before, before, limit)
	if err != nil {
		return nil, fmt.Errorf("查询维护审计: %w", err)
	}
	defer rows.Close()
	entries := make([]MaintenanceAuditEntry, 0, limit)
	for rows.Next() {
		var entry MaintenanceAuditEntry
		var reportsDeleted, auditDeleted sql.NullInt64
		if err := rows.Scan(&entry.ID, &entry.OccurredAt, &entry.Source, &entry.Action, &entry.TargetUserID, &entry.Result, &entry.ReportID,
			&reportsDeleted, &auditDeleted); err != nil {
			return nil, fmt.Errorf("读取维护审计: %w", err)
		}
		if reportsDeleted.Valid && auditDeleted.Valid {
			entry.Cleanup = &GovernanceCleanup{ReportsDeleted: reportsDeleted.Int64, AuditDeleted: auditDeleted.Int64}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取维护审计: %w", err)
	}
	return entries, nil
}
