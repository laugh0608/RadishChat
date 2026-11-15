package chat

import (
	"database/sql"
	"net/http"
)

const (
	botInstallationRead   = "installation:read"
	botInstallationHeader = "X-RadishChat-Bot-Installation"
	botCommandsReceive    = "commands:receive"
)

func botInstallationPermissions(receiveCommands bool) []string {
	permissions := []string{botInstallationRead}
	if receiveCommands {
		permissions = append(permissions, botCommandsReceive)
	}
	return permissions
}

func requestedBotPermissions(permissions []string) (receiveCommands, valid bool) {
	if len(permissions) == 1 && permissions[0] == botInstallationRead {
		return false, true
	}
	if len(permissions) == 2 &&
		((permissions[0] == botInstallationRead && permissions[1] == botCommandsReceive) ||
			(permissions[0] == botCommandsReceive && permissions[1] == botInstallationRead)) {
		return true, true
	}
	return false, false
}

type botInstallationGrant struct {
	InstallationID string   `json:"installationId,omitempty"`
	Permissions    []string `json:"permissions"`
}

type groupBotInstallation struct {
	botInstallationGrant
	BotID       string `json:"botId"`
	DisplayName string `json:"displayName"`
}

type botInstallationPreview struct {
	groupBotInstallation
	State string `json:"state,omitempty"`
}

type ownedBotInstallation struct {
	botInstallationGrant
	RoomID      int64  `json:"roomId"`
	RequestedBy string `json:"requestedBy"`
	State       string `json:"state"`
}

type botRoomGrant struct {
	botInstallationGrant
	BotID  string `json:"botId"`
	RoomID int64  `json:"roomId"`
}

func botGroupMembership(r *http.Request, tx *sql.Tx, roomID, userID int64) (membership, error) {
	member, err := access(r, tx, roomID, userID)
	if err == nil && member.roomKind != "group" {
		return member, errMissing
	}
	return member, err
}

func (s *Server) listGroupBotInstallations(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	roomID := intID(r.PathValue("room"))
	includePending := r.URL.Query().Get("includePending") == "1"
	installations := []botInstallationPreview{}
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		member, err := botGroupMembership(r, tx, roomID, u.ID)
		if err != nil {
			return err
		}
		if includePending && member.ownerID != u.ID {
			return errOwner
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,b.id,b.display_name,i.commands_receive,i.state
			FROM bot_installations i JOIN bots b ON b.id=i.bot_id
			WHERE i.room_id=? AND (i.state='active' OR ?) ORDER BY b.id`, roomID, includePending)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item botInstallationPreview
			var state string
			var receiveCommands bool
			if err := rows.Scan(&item.InstallationID, &item.BotID, &item.DisplayName, &receiveCommands, &state); err != nil {
				return err
			}
			if includePending {
				item.State = state
			}
			item.Permissions = botInstallationPermissions(receiveCommands)
			installations = append(installations, item)
		}
		return rows.Err()
	})
	if err != nil {
		botError(w, "list-group-bot-installations", err)
		return
	}
	reply(w, http.StatusOK, installations)
}

func (s *Server) groupBotInstallation(w http.ResponseWriter, r *http.Request, u user) {
	var requestedCommands bool
	if r.Method == http.MethodPut {
		var input struct {
			Permissions []string `json:"permissions"`
		}
		if !readJSON(w, r, &input) {
			return
		}
		var valid bool
		requestedCommands, valid = requestedBotPermissions(input.Permissions)
		if !valid {
			fail(w, http.StatusBadRequest, "安装须授予 installation:read，可选 commands:receive")
			return
		}
	}

	ctx := r.Context()
	roomID := intID(r.PathValue("room"))
	var view botInstallationPreview
	view.BotID = r.PathValue("bot")
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		member, err := botGroupMembership(r, tx, roomID, u.ID)
		if err != nil {
			return err
		}
		if member.ownerID != u.ID {
			return errOwner
		}

		var botOwnerID int64
		var receiveCommands bool
		err = tx.QueryRowContext(ctx, `SELECT b.display_name,b.owner_id,
			coalesce(i.id,''),coalesce(i.state,'available'),coalesce(i.commands_receive,0)
			FROM bots b LEFT JOIN bot_installations i ON i.bot_id=b.id AND i.room_id=?
			WHERE b.id=?`, roomID, view.BotID).
			Scan(&view.DisplayName, &botOwnerID, &view.InstallationID, &view.State, &receiveCommands)
		if err != nil {
			return err
		}
		view.Permissions = botInstallationPermissions(receiveCommands)
		if r.Method != http.MethodPut {
			return nil
		}
		if view.InstallationID != "" {
			if requestedCommands != receiveCommands {
				return errBotPermissionChange
			}
			return nil
		}

		state := "pending"
		if botOwnerID == u.ID {
			state = "active"
		}
		view.InstallationID = randomText(16)
		view.State = state
		view.Permissions = botInstallationPermissions(requestedCommands)
		_, err = tx.ExecContext(ctx, `INSERT INTO bot_installations(id,room_id,bot_id,requested_by,state,permission,commands_receive)
			VALUES (?,?,?,?,?,?,?)`, view.InstallationID, roomID, view.BotID, u.ID, state, botInstallationRead, requestedCommands)
		return err
	})
	if err != nil {
		botError(w, "group-bot-installation", err)
		return
	}
	reply(w, http.StatusOK, view)
}

func (s *Server) listOwnedBotInstallations(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	botID := r.PathValue("bot")
	installations := []ownedBotInstallation{}
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		var owned string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM bots WHERE id=? AND owner_id=?", botID, u.ID).Scan(&owned); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.room_id,u.username,i.state,i.commands_receive
			FROM bot_installations i JOIN users u ON u.id=i.requested_by
			JOIN rooms r ON r.id=i.room_id AND r.kind='group'
			WHERE i.bot_id=? ORDER BY i.room_id`, botID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item ownedBotInstallation
			var receiveCommands bool
			if err := rows.Scan(&item.InstallationID, &item.RoomID, &item.RequestedBy, &item.State, &receiveCommands); err != nil {
				return err
			}
			item.Permissions = botInstallationPermissions(receiveCommands)
			installations = append(installations, item)
		}
		return rows.Err()
	})
	if err != nil {
		botError(w, "list-owned-bot-installations", err)
		return
	}
	reply(w, http.StatusOK, installations)
}

func (s *Server) approveBotInstallation(w http.ResponseWriter, r *http.Request, u user) {
	result, err := s.db.ExecContext(r.Context(), `UPDATE bot_installations SET state='active' WHERE id=(
		SELECT i.id FROM bot_installations i
		JOIN bots b ON b.id=i.bot_id
		JOIN rooms r ON r.id=i.room_id AND r.kind='group'
		LEFT JOIN members requester ON requester.room_id=i.room_id AND requester.user_id=i.requested_by
		WHERE i.id=? AND i.bot_id=? AND b.owner_id=?
		  AND (i.state='active' OR (r.owner_id=i.requested_by AND requester.user_id IS NOT NULL))
	)`, r.PathValue("installation"), r.PathValue("bot"), u.ID)
	if err != nil {
		botError(w, "approve-bot-installation", err)
		return
	}
	updated, err := result.RowsAffected()
	if err != nil {
		botError(w, "approve-bot-installation", err)
		return
	}
	if updated == 0 {
		botError(w, "approve-bot-installation", errMissing)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeBotInstallation(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	installationID := r.PathValue("installation")
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		var roomID, botOwnerID int64
		var state string
		err := tx.QueryRowContext(ctx, `SELECT i.room_id,b.owner_id,i.state
			FROM bot_installations i JOIN bots b ON b.id=i.bot_id
			JOIN rooms r ON r.id=i.room_id AND r.kind='group' WHERE i.id=?`, installationID).
			Scan(&roomID, &botOwnerID, &state)
		if err != nil {
			return err
		}
		if u.ID != botOwnerID {
			member, err := botGroupMembership(r, tx, roomID, u.ID)
			if err != nil {
				return err
			}
			if member.ownerID != u.ID {
				if state == "pending" {
					return errMissing
				}
				return errOwner
			}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM bot_installations WHERE id=?", installationID)
		return err
	})
	if err != nil {
		botError(w, "remove-bot-installation", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) botRoomInstallation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var grant botRoomGrant
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		grant, err = botInstallationForToken(r, tx)
		return err
	})
	if err != nil {
		botError(w, "bot-room-installation", err)
		return
	}
	reply(w, http.StatusOK, grant)
}

func botInstallationForToken(r *http.Request, tx *sql.Tx) (botRoomGrant, error) {
	var grant botRoomGrant
	identity, err := botIdentity(r, tx)
	if err != nil {
		return grant, err
	}
	grant.RoomID = intID(r.PathValue("room"))
	grant.BotID = identity.ID
	expected := r.Header.Get(botInstallationHeader)
	var receiveCommands bool
	err = tx.QueryRowContext(r.Context(), `SELECT i.id,i.commands_receive FROM bot_installations i
		JOIN rooms r ON r.id=i.room_id AND r.kind='group'
		WHERE i.room_id=? AND i.bot_id=? AND i.state='active' AND i.permission=? AND (?='' OR i.id=?)`,
		grant.RoomID, identity.ID, botInstallationRead, expected, expected).
		Scan(&grant.InstallationID, &receiveCommands)
	grant.Permissions = botInstallationPermissions(receiveCommands)
	return grant, err
}
