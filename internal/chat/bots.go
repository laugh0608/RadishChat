package chat

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

const (
	botTokenPrefix = "bot_"
	botTokenBytes  = 32
)

var errBotCredential = errors.New("invalid Bot credential")
var errBotPermissionChange = errors.New("existing Bot installation has different permissions")

type bot struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (s *Server) listBots(w http.ResponseWriter, r *http.Request, owner user) {
	bots := []bot{}
	err := s.userTransaction(r, owner.ID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(r.Context(), `SELECT id,display_name FROM bots WHERE owner_id=? ORDER BY id`, owner.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record bot
			if err := rows.Scan(&record.ID, &record.DisplayName); err != nil {
				return err
			}
			bots = append(bots, record)
		}
		return rows.Err()
	})
	if err != nil {
		userError(w, "list-bots", err)
		return
	}
	reply(w, http.StatusOK, bots)
}

func (s *Server) createBot(w http.ResponseWriter, r *http.Request, owner user) {
	var input struct {
		DisplayName string `json:"displayName"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.DisplayName)
	if !validLabel(name, 48) {
		fail(w, http.StatusBadRequest, "Bot 名称需为 1–48 个字符，不能包含控制字符")
		return
	}
	record := bot{ID: randomText(16), DisplayName: name}
	err := s.userTransaction(r, owner.ID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), `INSERT INTO bots(id,owner_id,display_name) VALUES (?,?,?)`, record.ID, owner.ID, record.DisplayName)
		return err
	})
	if err != nil {
		userError(w, "create-bot", err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (s *Server) rotateBotCredential(w http.ResponseWriter, r *http.Request, owner user) {
	token := botTokenPrefix + randomText(botTokenBytes)
	if s.updateBotCredential(w, r, owner.ID, digest(token)) {
		reply(w, http.StatusOK, map[string]string{"token": token})
	}
}

func (s *Server) revokeBotCredential(w http.ResponseWriter, r *http.Request, owner user) {
	if s.updateBotCredential(w, r, owner.ID, nil) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) updateBotCredential(w http.ResponseWriter, r *http.Request, ownerID int64, tokenHash any) bool {
	err := s.userTransaction(r, ownerID, func(tx *sql.Tx) error {
		var id string
		return tx.QueryRowContext(r.Context(), `UPDATE bots SET token_hash=? WHERE id=? AND owner_id=? RETURNING id`,
			tokenHash, r.PathValue("bot"), ownerID).Scan(&id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "Bot 不存在")
		return false
	}
	if err != nil {
		userError(w, "update-bot-credential", err)
		return false
	}
	return true
}

func botToken(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	scheme, value, _ := strings.Cut(values[0], " ")
	value = strings.TrimLeft(value, " ")
	if !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(value, botTokenPrefix) ||
		len(value) != len(botTokenPrefix)+base64.RawURLEncoding.EncodedLen(botTokenBytes) {
		return ""
	}
	return value
}

func botIdentity(r *http.Request, db rowQuerier) (bot, error) {
	var record bot
	token := botToken(r)
	if token == "" {
		return record, errBotCredential
	}
	err := db.QueryRowContext(r.Context(), `SELECT b.id,b.display_name FROM bots b JOIN users owner ON owner.id=b.owner_id
		WHERE b.token_hash=? AND owner.status<>'disabled'`, digest(token)).Scan(&record.ID, &record.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return record, errBotCredential
	}
	return record, err
}

func (s *Server) botMe(w http.ResponseWriter, r *http.Request) {
	record, err := botIdentity(r, s.db)
	if err != nil {
		botError(w, "bot-identity", err)
		return
	}
	reply(w, http.StatusOK, record)
}

func botError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, errBotCredential):
		w.Header().Set("WWW-Authenticate", `Bearer realm="RadishChat Bot"`)
		fail(w, http.StatusUnauthorized, "Bot 凭据无效或缺失")
	case errors.Is(err, errMissing), errors.Is(err, sql.ErrNoRows):
		fail(w, http.StatusNotFound, "Bot、群组或安装授权不存在")
	case errors.Is(err, errOwner):
		fail(w, http.StatusForbidden, "没有管理此 Bot 安装的权限")
	case errors.Is(err, errBotPermissionChange):
		fail(w, http.StatusConflict, "修改权限需先移除现有安装并重新授权")
	default:
		userError(w, operation, err)
	}
}
