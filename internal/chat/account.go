package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const recoveryCodeBytes = 32
const recoveryCodeLifetime = 15 * time.Minute

// DisableLocalAccount revokes a local account and its delegated Bot grants in an existing instance.
func DisableLocalAccount(ctx context.Context, database string, id int64) error {
	return maintainLocalAccount(ctx, database, id, "disabled", [3]string{"active", "recovery_required", "disabled"}, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM account_recovery_codes WHERE user_id=?", id); err != nil {
			return fmt.Errorf("撤销账号恢复码: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE bots SET token_hash=NULL WHERE owner_id=? AND token_hash IS NOT NULL", id); err != nil {
			return fmt.Errorf("撤销所属 Bot 凭据: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bot_installations
			WHERE bot_id IN (SELECT id FROM bots WHERE owner_id=?)
			   OR (state='pending' AND requested_by=?)`, id, id); err != nil {
			return fmt.Errorf("撤销 Bot 安装及待审批请求: %w", err)
		}
		return nil
	})
}

// IssueLocalAccountRecovery moves an account into recovery and returns the new code after commit.
// Reopening is an explicit transition reserved for disabled accounts.
func IssueLocalAccountRecovery(ctx context.Context, database string, id int64, reopen bool) (string, error) {
	allowed := [3]string{"active", "recovery_required"}
	if reopen {
		allowed = [3]string{"disabled"}
	}
	code := randomText(recoveryCodeBytes)
	err := maintainLocalAccount(ctx, database, id, "recovery_required", allowed, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO account_recovery_codes(user_id,token_hash,expires)
			VALUES (?,?,?) ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires=excluded.expires`,
			id, digest(code), time.Now().Add(recoveryCodeLifetime).Unix())
		if err != nil {
			return fmt.Errorf("保存账号恢复码: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

func maintainLocalAccount(ctx context.Context, database string, id int64, status string, allowed [3]string, apply func(*sql.Tx) error) error {
	if id <= 0 {
		return errors.New("用户 ID 必须为正整数")
	}
	path, err := filepath.Abs(database)
	if err != nil {
		return fmt.Errorf("解析数据库路径: %w", err)
	}
	db, err := sqliteDatabase(path, "rw")
	if err != nil {
		return fmt.Errorf("打开现有数据库: %w", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取现有数据库版本: %w", err)
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("数据库版本为 v%d，账号维护要求 v%d", version, currentSchemaVersion)
	}
	return transaction(ctx, db, func(tx *sql.Tx) error {
		var target int64
		err := tx.QueryRowContext(ctx, `UPDATE users SET status=?
			WHERE id=? AND password IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM external_accounts WHERE user_id=users.id)
			  AND status IN (?,?,?)
			RETURNING id`, status, id, allowed[0], allowed[1], allowed[2]).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			var current string
			var local bool
			err := tx.QueryRowContext(ctx, `SELECT status,password IS NOT NULL AND
				NOT EXISTS (SELECT 1 FROM external_accounts WHERE user_id=users.id) FROM users WHERE id=?`, id).Scan(&current, &local)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("账号 %d 不存在", id)
			}
			if err != nil {
				return fmt.Errorf("检查账号 %d 的类型: %w", id, err)
			}
			if !local {
				return fmt.Errorf("账号 %d 不是本地密码账号", id)
			}
			return fmt.Errorf("账号 %d 当前状态 %s 不允许此操作", id, current)
		}
		if err != nil {
			return fmt.Errorf("更新账号 %d 的状态: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", target); err != nil {
			return fmt.Errorf("撤销账号会话: %w", err)
		}
		return apply(tx)
	})
}
