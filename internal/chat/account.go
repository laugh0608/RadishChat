package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// DisableLocalAccount revokes a local account and its delegated Bot grants in an existing instance.
func DisableLocalAccount(ctx context.Context, database string, id int64) error {
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
		err := tx.QueryRowContext(ctx, `UPDATE users SET status='disabled'
			WHERE id=? AND password IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM external_accounts WHERE user_id=users.id)
			RETURNING id`, id).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", id).Scan(&exists); err != nil {
				return fmt.Errorf("检查账号 %d 的类型: %w", id, err)
			}
			if !exists {
				return fmt.Errorf("账号 %d 不存在", id)
			}
			return fmt.Errorf("账号 %d 不是本地密码账号", id)
		}
		if err != nil {
			return fmt.Errorf("停用账号 %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", target); err != nil {
			return fmt.Errorf("撤销账号会话: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM account_recovery_codes WHERE user_id=?", target); err != nil {
			return fmt.Errorf("撤销账号恢复码: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE bots SET token_hash=NULL WHERE owner_id=? AND token_hash IS NOT NULL", target); err != nil {
			return fmt.Errorf("撤销所属 Bot 凭据: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bot_installations
			WHERE bot_id IN (SELECT id FROM bots WHERE owner_id=?)
			   OR (state='pending' AND requested_by=?)`, target, target); err != nil {
			return fmt.Errorf("撤销 Bot 安装及待审批请求: %w", err)
		}
		return nil
	})
}
