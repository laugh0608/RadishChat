package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	botSchemaVersion             = 4
	botInstallationSchemaVersion = 5
	currentSchemaVersion         = 7
)

const schemaV1 = `
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  password TEXT
);
CREATE TABLE external_accounts (
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  user_id INTEGER NOT NULL UNIQUE REFERENCES users(id),
  PRIMARY KEY (issuer, subject)
);
CREATE TABLE sessions (
  token_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  expires INTEGER NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires);
CREATE TABLE oidc_flows (
  state TEXT PRIMARY KEY,
  browser_hash BLOB NOT NULL,
  nonce TEXT NOT NULL,
  verifier TEXT NOT NULL,
  expires INTEGER NOT NULL
);
CREATE INDEX oidc_flows_expiry ON oidc_flows(expires);
CREATE TABLE rooms (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('direct','group')),
  name TEXT NOT NULL DEFAULT '',
  owner_id INTEGER REFERENCES users(id),
  direct_a INTEGER REFERENCES users(id),
  direct_b INTEGER REFERENCES users(id),
  UNIQUE(direct_a, direct_b),
  CHECK ((kind = 'direct' AND owner_id IS NULL AND direct_a IS NOT NULL AND direct_b IS NOT NULL AND direct_a < direct_b)
      OR (kind = 'group' AND owner_id IS NOT NULL AND direct_a IS NULL AND direct_b IS NULL))
);
CREATE TABLE members (
  room_id INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id),
  PRIMARY KEY (room_id, user_id)
);
CREATE INDEX members_user ON members(user_id, room_id);
CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  room_id INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  sender_id INTEGER NOT NULL REFERENCES users(id),
  body TEXT NOT NULL,
  sent_at INTEGER NOT NULL,
  request_id TEXT NOT NULL,
  UNIQUE(room_id, sender_id, request_id)
);
CREATE INDEX messages_room ON messages(room_id, id DESC);
PRAGMA user_version = 1;
`

const migrateV1ToV2 = `
ALTER TABLE members ADD COLUMN joined_after_message_id INTEGER NOT NULL DEFAULT 0 CHECK (joined_after_message_id >= 0);
UPDATE members SET joined_after_message_id = coalesce(
  (SELECT max(id) FROM messages WHERE room_id = members.room_id), 0
) WHERE room_id IN (SELECT id FROM rooms WHERE kind = 'group');
PRAGMA user_version = 2;
`

const migrateV2ToV3 = `
ALTER TABLE members ADD COLUMN membership_id TEXT NOT NULL DEFAULT '';
UPDATE members SET membership_id = lower(hex(randomblob(16)));
PRAGMA user_version = 3;
`

const migrateV3ToV4 = `
CREATE TABLE bots (
  id TEXT PRIMARY KEY NOT NULL,
  owner_id INTEGER NOT NULL REFERENCES users(id),
  display_name TEXT NOT NULL,
  token_hash BLOB UNIQUE CHECK (token_hash IS NULL OR (typeof(token_hash) = 'blob' AND length(token_hash) = 32))
);
CREATE INDEX bots_owner ON bots(owner_id, id);
PRAGMA user_version = 4;
`

const migrateV4ToV5 = `
CREATE TABLE bot_installations (
  id TEXT PRIMARY KEY NOT NULL,
  room_id INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  requested_by INTEGER NOT NULL REFERENCES users(id),
  state TEXT NOT NULL CHECK (state IN ('pending','active')),
  permission TEXT NOT NULL CHECK (permission = 'installation:read'),
  UNIQUE (room_id, bot_id)
);
CREATE INDEX bot_installations_bot ON bot_installations(bot_id, room_id);
PRAGMA user_version = 5;
`

const migrateV5ToV6 = `
ALTER TABLE bot_installations ADD COLUMN commands_receive INTEGER NOT NULL DEFAULT 0 CHECK (commands_receive IN (0,1));
ALTER TABLE bot_installations ADD COLUMN last_event_sequence INTEGER NOT NULL DEFAULT 0 CHECK (last_event_sequence >= 0);
ALTER TABLE bot_installations ADD COLUMN pruned_event_sequence INTEGER NOT NULL DEFAULT 0
  CHECK (pruned_event_sequence BETWEEN 0 AND last_event_sequence);
CREATE TABLE bot_events (
  installation_id TEXT NOT NULL REFERENCES bot_installations(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  occurred_at INTEGER NOT NULL,
  kind TEXT NOT NULL,
  version INTEGER NOT NULL CHECK (version > 0),
  command TEXT NOT NULL,
  PRIMARY KEY (installation_id, sequence)
);
CREATE INDEX bot_events_expiry ON bot_events(occurred_at);
CREATE TABLE bot_command_requests (
  installation_id TEXT NOT NULL REFERENCES bot_installations(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL,
  membership_id TEXT NOT NULL,
  request_hash BLOB NOT NULL,
  command_hash BLOB NOT NULL,
  event_sequence INTEGER NOT NULL CHECK (event_sequence > 0),
  accepted_at INTEGER NOT NULL,
  PRIMARY KEY (installation_id, user_id, membership_id, request_hash)
);
CREATE INDEX bot_command_requests_expiry ON bot_command_requests(accepted_at);
PRAGMA user_version = 6;
`

const migrateV6ToV7 = `
ALTER TABLE bot_installations ADD COLUMN commands_reply INTEGER NOT NULL DEFAULT 0
  CHECK (commands_reply IN (0,1) AND commands_reply <= commands_receive);
ALTER TABLE messages RENAME TO messages_v6;
CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  room_id INTEGER NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  sender_id INTEGER REFERENCES users(id),
  sender_bot_id TEXT REFERENCES bots(id),
  body TEXT NOT NULL,
  sent_at INTEGER NOT NULL,
  request_id TEXT NOT NULL,
  CHECK ((sender_id IS NULL) != (sender_bot_id IS NULL)),
  UNIQUE (room_id, sender_id, request_id),
  UNIQUE (room_id, sender_bot_id, request_id)
);
INSERT INTO messages (id,room_id,sender_id,body,sent_at,request_id)
  SELECT id,room_id,sender_id,body,sent_at,request_id FROM messages_v6;
DROP TABLE messages_v6;
CREATE INDEX messages_room ON messages(room_id, id DESC);
PRAGMA user_version = 7;
`

func openDB(path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sqliteDatabase(path, "rw")
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	err = transaction(ctx, db, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		switch version {
		case 0:
			if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
				return fmt.Errorf("SQLite 初始化失败: %w", err)
			}
			fallthrough
		case 1:
			if _, err := tx.ExecContext(ctx, migrateV1ToV2); err != nil {
				return fmt.Errorf("SQLite 从 v1 迁移到 v2 失败: %w", err)
			}
			fallthrough
		case 2:
			if _, err := tx.ExecContext(ctx, migrateV2ToV3); err != nil {
				return fmt.Errorf("SQLite 从 v2 迁移到 v3 失败: %w", err)
			}
			fallthrough
		case 3:
			if _, err := tx.ExecContext(ctx, migrateV3ToV4); err != nil {
				return fmt.Errorf("SQLite 从 v3 迁移到 v4 失败: %w", err)
			}
			fallthrough
		case 4:
			if _, err := tx.ExecContext(ctx, migrateV4ToV5); err != nil {
				return fmt.Errorf("SQLite 从 v4 迁移到 v5 失败: %w", err)
			}
			fallthrough
		case 5:
			if _, err := tx.ExecContext(ctx, migrateV5ToV6); err != nil {
				return fmt.Errorf("SQLite 从 v5 迁移到 v6 失败: %w", err)
			}
			fallthrough
		case 6:
			if _, err := tx.ExecContext(ctx, migrateV6ToV7); err != nil {
				return fmt.Errorf("SQLite 从 v6 迁移到 v7 失败: %w", err)
			}
			return nil
		case currentSchemaVersion:
			return nil
		default:
			return fmt.Errorf("未支持的数据库版本 %d", version)
		}
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func sqliteDatabase(path, mode string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {mode}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// 单实例使用一条连接；事务不得另行从 DB 取第二条连接。
	db.SetMaxOpenConns(1)
	return db, nil
}

func transaction(ctx context.Context, db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func recordConflict(err error) bool {
	var sqliteErr interface{ Code() int }
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067 // SQLITE_CONSTRAINT_UNIQUE
}
