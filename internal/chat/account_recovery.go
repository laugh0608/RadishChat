package chat

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
)

const validRecoveryProof = `status='recovery_required' AND password IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM external_accounts WHERE user_id=users.id)
	AND EXISTS (SELECT 1 FROM account_recovery_codes
		WHERE user_id=users.id AND token_hash=? AND expires>unixepoch())`

const invalidRecoveryMessage = "用户名或恢复码无效，请向实例维护者核对"

type localRecoveryRequest struct {
	Username     string `json:"username"`
	RecoveryCode string `json:"recoveryCode"`
	NewPassword  string `json:"newPassword"`
}

func (s *Server) recoverLocalAccount(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, "recover:"+s.clientHost(r), 35) {
		return
	}
	var input localRecoveryRequest
	if !readJSON(w, r, &input) {
		return
	}
	username := normalizeUser(input.Username)
	if !usernamePattern.MatchString(username) {
		fail(w, http.StatusUnauthorized, invalidRecoveryMessage)
		return
	}
	if !s.limit(w, "recover-user:"+username, 12) {
		return
	}
	if len(input.RecoveryCode) != base64.RawURLEncoding.EncodedLen(recoveryCodeBytes) {
		fail(w, http.StatusUnauthorized, invalidRecoveryMessage)
		return
	}

	ctx := r.Context()
	codeHash := digest(input.RecoveryCode)
	var id int64
	var currentHash string
	err := s.db.QueryRowContext(ctx, "SELECT id,password FROM users WHERE username=? AND "+validRecoveryProof,
		username, codeHash).Scan(&id, &currentHash)
	if err != nil {
		recoveryError(w, err)
		return
	}
	if !validPassword(input.NewPassword) {
		fail(w, http.StatusBadRequest, "新密码至少 10 个字符且不超过 256 字节")
		return
	}
	if !s.acquirePassword(w) {
		return
	}
	if verifyPassword(input.NewPassword, currentHash) {
		<-s.gate
		fail(w, http.StatusBadRequest, "新密码须与当前密码不同")
		return
	}
	newHash := hashPassword(input.NewPassword)
	<-s.gate

	err = transaction(ctx, s.db, func(tx *sql.Tx) error {
		// Recheck the proof after hashing. The first write also serializes against maintenance commands.
		var target int64
		if err := tx.QueryRowContext(ctx, `UPDATE users SET password=?,status='active'
			WHERE id=? AND password=? AND `+validRecoveryProof+` RETURNING id`, newHash, id, currentHash, codeHash).Scan(&target); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM account_recovery_codes WHERE user_id=?", target); err != nil {
			return fmt.Errorf("消费账号恢复码: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", target); err != nil {
			return fmt.Errorf("撤销账号会话: %w", err)
		}
		return nil
	})
	if err != nil {
		recoveryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func recoveryError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusUnauthorized, invalidRecoveryMessage)
		return
	}
	internal(w, "recover-local-account", err)
}
