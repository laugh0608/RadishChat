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

const schema = `
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
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// 单实例使用一条写连接；事务不得另行从 DB 取第二条连接。
	db.SetMaxOpenConns(1)
	err = transaction(context.Background(), db, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		switch version {
		case 0:
			_, err := tx.Exec(schema)
			return err
		case 1:
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
