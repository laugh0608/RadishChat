package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type exportedAccount struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
}

type exportedReport struct {
	ID        int64  `json:"id"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"createdAt"`
	Status    string `json:"status"`
	ClosedAt  int64  `json:"closedAt,omitempty"`
}

type accountExport struct {
	Account exportedAccount  `json:"account"`
	Reports []exportedReport `json:"reports"`
}

// ExportAccountData publishes a private file only after its generation has been audited.
// The maintenance operator supplies the username already verified with the requester.
func ExportAccountData(ctx context.Context, database string, id int64, verifiedUsername, destination string) error {
	username := normalizeUser(verifiedUsername)
	if id <= 0 || !usernamePattern.MatchString(username) || destination == "" {
		return errors.New("导出须指定正整数账号 ID、已核实的用户名和输出文件")
	}
	path, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("解析导出路径: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("导出文件已存在: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查导出路径: %w", err)
	}
	db, err := openMaintenanceDB(ctx, database, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	file, err := os.CreateTemp(filepath.Dir(path), ".radishchat-export-*")
	if err != nil {
		return fmt.Errorf("创建受保护的临时导出文件: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	err = transaction(ctx, db, func(tx *sql.Tx) error {
		export, err := readAccountExport(ctx, tx, id, username)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(file).Encode(export); err != nil {
			return fmt.Errorf("生成账号导出: %w", err)
		}
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return fmt.Errorf("同步账号导出: %w", err)
		}
		return appendMaintenanceAudit(ctx, tx, auditDataExport, id, auditSuccess, 0)
	})
	if err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return fmt.Errorf("发布账号导出: %w", err)
	}
	return nil
}

func readAccountExport(ctx context.Context, tx *sql.Tx, id int64, username string) (accountExport, error) {
	export := accountExport{Reports: []exportedReport{}}
	account := &export.Account
	err := tx.QueryRowContext(ctx, "SELECT id,username,display_name,status FROM users WHERE id=? AND username=?", id, username).
		Scan(&account.ID, &account.Username, &account.DisplayName, &account.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return accountExport{}, errors.New("已核实的用户名与目标账号不匹配")
	}
	if err != nil {
		return accountExport{}, fmt.Errorf("核对导出账号: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,reason,created_at,`+reportStatus+`,coalesce(closed_at,0)
		FROM message_reports WHERE reporter_user_id=? AND (closed_at IS NULL OR closed_at>?) ORDER BY id`,
		id, time.Now().Add(-closedReportRetention).UnixMilli())
	if err != nil {
		return accountExport{}, fmt.Errorf("查询本人举报: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var report exportedReport
		if err := rows.Scan(&report.ID, &report.Reason, &report.CreatedAt, &report.Status, &report.ClosedAt); err != nil {
			return accountExport{}, fmt.Errorf("读取本人举报: %w", err)
		}
		export.Reports = append(export.Reports, report)
	}
	if err := rows.Err(); err != nil {
		return accountExport{}, fmt.Errorf("读取本人举报: %w", err)
	}
	return export, nil
}

type GovernanceCleanup struct {
	ReportsDeleted int64 `json:"reportsDeleted"`
	AuditDeleted   int64 `json:"auditDeleted"`
}

// PruneGovernanceData applies both retention periods and records the committed counts.
func PruneGovernanceData(ctx context.Context, database string) (GovernanceCleanup, error) {
	db, err := openMaintenanceDB(ctx, database, "rw")
	if err != nil {
		return GovernanceCleanup{}, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA secure_delete = ON"); err != nil {
		return GovernanceCleanup{}, fmt.Errorf("启用 SQLite 清理设置: %w", err)
	}
	var cleanup GovernanceCleanup
	err = transaction(ctx, db, func(tx *sql.Tx) error {
		now := time.Now()
		result, err := tx.ExecContext(ctx, "DELETE FROM message_reports WHERE closed_at<=?", now.Add(-closedReportRetention).UnixMilli())
		if err != nil {
			return fmt.Errorf("清理到期举报: %w", err)
		}
		cleanup.ReportsDeleted, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("统计清理举报: %w", err)
		}
		result, err = tx.ExecContext(ctx, "DELETE FROM maintenance_audit WHERE occurred_at<=?", now.Add(-maintenanceAuditRetention).UnixMilli())
		if err != nil {
			return fmt.Errorf("清理到期审计: %w", err)
		}
		cleanup.AuditDeleted, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("统计清理审计: %w", err)
		}
		return writeMaintenanceAudit(ctx, tx, MaintenanceAuditEntry{
			OccurredAt: now.UnixMilli(), Action: auditDataPrune, Result: auditSuccess, Cleanup: &cleanup,
		})
	})
	if err != nil {
		return GovernanceCleanup{}, err
	}
	return cleanup, nil
}
