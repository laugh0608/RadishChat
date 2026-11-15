package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CopyDatabase 写入一份保留数据库版本和业务数据、清除认证状态及 Bot 授权的副本。
func CopyDatabase(ctx context.Context, source, destination string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("读取源数据库: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("源数据库必须是普通文件")
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		name := destination + suffix
		if _, err := os.Lstat(name); err == nil {
			return fmt.Errorf("目标数据库或辅助文件已存在: %s: %w", name, os.ErrExist)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查目标数据库: %w", err)
		}
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建目标目录: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".radishchat-copy-*.sqlite")
	if err != nil {
		return fmt.Errorf("创建数据库临时文件: %w", err)
	}
	path := temp.Name()
	defer os.Remove(path)
	if err := temp.Close(); err != nil {
		return err
	}
	if err := snapshotDatabase(ctx, source, path); err != nil {
		return fmt.Errorf("生成 SQLite 快照: %w", err)
	}
	if err := prepareDatabaseCopy(ctx, path); err != nil {
		return fmt.Errorf("验证或清理 SQLite 快照: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("同步 SQLite 快照: %w", err)
	}
	// 在同一目录发布已验证的文件。若另一个进程先创建了目标，链接会失败。
	if err := os.Link(path, destination); err != nil {
		return fmt.Errorf("保存 SQLite 副本: %w", err)
	}
	return nil
}

func snapshotDatabase(ctx context.Context, source, path string) error {
	db, err := sqliteDatabase(source, "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 1 || version > currentSchemaVersion {
		return fmt.Errorf("未支持的数据库版本 %d", version)
	}
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

func prepareDatabaseCopy(ctx context.Context, path string) error {
	db, err := sqliteDatabase(path, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA secure_delete = ON"); err != nil {
		return err
	}
	return transaction(ctx, db, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions; DELETE FROM oidc_flows"); err != nil {
			return err
		}
		if version >= botSchemaVersion {
			if _, err := tx.ExecContext(ctx, "UPDATE bots SET token_hash=NULL WHERE token_hash IS NOT NULL"); err != nil {
				return fmt.Errorf("清除 Bot 凭据: %w", err)
			}
		}
		if version >= botInstallationSchemaVersion {
			if _, err := tx.ExecContext(ctx, "DELETE FROM bot_installations"); err != nil {
				return fmt.Errorf("清除 Bot 安装授权: %w", err)
			}
		}
		var integrity string
		if err := tx.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
			return err
		}
		if integrity != "ok" {
			return errors.New("SQLite 完整性检查未通过")
		}
		var invalidReference bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pragma_foreign_key_check)").Scan(&invalidReference); err != nil {
			return err
		}
		if invalidReference {
			return errors.New("SQLite 外键检查未通过")
		}
		return nil
	})
}
