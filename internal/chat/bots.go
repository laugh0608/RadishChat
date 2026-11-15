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

type bot struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (s *Server) listBots(w http.ResponseWriter, r *http.Request, owner user) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,display_name FROM bots WHERE owner_id=? ORDER BY id`, owner.ID)
	if err != nil {
		internal(w, "list-bots", err)
		return
	}
	defer rows.Close()
	bots := []bot{}
	for rows.Next() {
		var record bot
		if err := rows.Scan(&record.ID, &record.DisplayName); err != nil {
			internal(w, "list-bots", err)
			return
		}
		bots = append(bots, record)
	}
	if err := rows.Err(); err != nil {
		internal(w, "list-bots", err)
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
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO bots(id,owner_id,display_name) VALUES (?,?,?)`, record.ID, owner.ID, record.DisplayName); err != nil {
		internal(w, "create-bot", err)
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
	result, err := s.db.ExecContext(r.Context(), `UPDATE bots SET token_hash=? WHERE id=? AND owner_id=?`, tokenHash, r.PathValue("bot"), ownerID)
	if err != nil {
		internal(w, "update-bot-credential", err)
		return false
	}
	updated, err := result.RowsAffected()
	if err != nil {
		internal(w, "update-bot-credential", err)
		return false
	}
	if updated == 0 {
		fail(w, http.StatusNotFound, "Bot 不存在")
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

func (s *Server) botMe(w http.ResponseWriter, r *http.Request) {
	token := botToken(r)
	if token == "" {
		rejectBotCredential(w)
		return
	}
	var record bot
	err := s.db.QueryRowContext(r.Context(), `SELECT id,display_name FROM bots WHERE token_hash=?`, digest(token)).Scan(&record.ID, &record.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		rejectBotCredential(w)
		return
	}
	if err != nil {
		internal(w, "bot-identity", err)
		return
	}
	reply(w, http.StatusOK, record)
}

func rejectBotCredential(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="RadishChat Bot"`)
	fail(w, http.StatusUnauthorized, "Bot 凭据无效或缺失")
}
