package chat

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

var errMissing = errors.New("missing")
var errOwner = errors.New("not owner")
var errMember = errors.New("already member")
var errGroupFull = errors.New("group full")
var errRequestConflict = errors.New("request conflict")

const membershipHeader = "X-RadishChat-Membership"

type room struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	PeerUsername  string `json:"peerUsername,omitempty"`
	OwnerID       int64  `json:"ownerId,omitempty"`
	MemberCount   int    `json:"memberCount"`
	LastMessageID int64  `json:"lastMessageId"`
	MembershipID  string `json:"membershipId"`
}
type message struct {
	ID       int64  `json:"id"`
	Author   user   `json:"author"`
	Markdown string `json:"markdown"`
	SentAt   int64  `json:"sentAt"`
}
type messagePage struct {
	Messages []message `json:"messages"`
	More     bool      `json:"more"`
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request, u user) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT r.id,r.kind,coalesce(peer.display_name,r.name),coalesce(peer.username,''),coalesce(r.owner_id,0),
			(SELECT count(*) FROM members WHERE room_id=r.id),
			coalesce((SELECT id FROM messages WHERE room_id=r.id AND id>m.joined_after_message_id ORDER BY id DESC LIMIT 1),0) AS last,
			m.membership_id
		FROM members m JOIN rooms r ON r.id=m.room_id
		LEFT JOIN users peer ON peer.id=CASE WHEN r.direct_a=m.user_id THEN r.direct_b ELSE r.direct_a END
		WHERE m.user_id=? ORDER BY last DESC,r.id DESC`, u.ID)
	if err != nil {
		internal(w, "list-rooms", err)
		return
	}
	defer rows.Close()
	result := make([]room, 0)
	for rows.Next() {
		var c room
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.PeerUsername, &c.OwnerID, &c.MemberCount, &c.LastMessageID, &c.MembershipID); err != nil {
			internal(w, "room-row", err)
			return
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		internal(w, "room-rows", err)
		return
	}
	reply(w, 200, result)
}

func findUser(ctx context.Context, tx *sql.Tx, username string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE username=?", normalizeUser(username)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errMissing
	}
	return id, err
}

func (s *Server) directRoom(w http.ResponseWriter, r *http.Request, u user) {
	var input struct {
		Username string `json:"username"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	input.Username = normalizeUser(input.Username)
	if !usernamePattern.MatchString(input.Username) || input.Username == u.Username {
		fail(w, 400, "请输入对方的完整用户名")
		return
	}
	ctx := r.Context()
	var id, other int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		other, err = findUser(ctx, tx, input.Username)
		if err != nil {
			return err
		}
		a, b := min(u.ID, other), max(u.ID, other)
		if _, err := tx.ExecContext(ctx, `INSERT INTO rooms(kind,direct_a,direct_b) VALUES ('direct',?,?) ON CONFLICT(direct_a,direct_b) DO NOTHING`, a, b); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT id FROM rooms WHERE direct_a=? AND direct_b=?", a, b).Scan(&id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO members(room_id,user_id,membership_id)
			VALUES (?,?,lower(hex(randomblob(16)))),(?,?,lower(hex(randomblob(16))))
			ON CONFLICT(room_id,user_id) DO NOTHING`, id, a, id, b)
		return err
	})
	if errors.Is(err, errMissing) {
		fail(w, 404, "此服务器上没有这个用户")
		return
	}
	if err != nil {
		internal(w, "direct-room", err)
		return
	}
	s.events.notify(u.ID, other)
	reply(w, 200, map[string]int64{"id": id})
}

func (s *Server) groupRoom(w http.ResponseWriter, r *http.Request, u user) {
	var input struct {
		Name      string   `json:"name"`
		Usernames []string `json:"usernames"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validLabel(input.Name, 64) || len(input.Usernames) > 49 {
		fail(w, 400, "群名需为 1–64 字，群最多 50 人")
		return
	}
	ctx := r.Context()
	var id int64
	people := []int64{u.ID}
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		seen := map[int64]bool{u.ID: true}
		for _, name := range input.Usernames {
			member, err := findUser(ctx, tx, name)
			if err != nil {
				return err
			}
			if !seen[member] {
				people = append(people, member)
				seen[member] = true
			}
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO rooms(kind,name,owner_id) VALUES ('group',?,?)", input.Name, u.ID)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, member := range people {
			if _, err := tx.ExecContext(ctx, `INSERT INTO members(room_id,user_id,membership_id)
				VALUES (?,?,lower(hex(randomblob(16))))`, id, member); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errMissing) {
		fail(w, 404, "至少有一个用户名在此服务器上不存在")
		return
	}
	if err != nil {
		internal(w, "group-room", err)
		return
	}
	s.events.notify(people...)
	reply(w, 201, map[string]int64{"id": id})
}

type membership struct {
	ownerID              int64
	joinedAfterMessageID int64
}

func access(r *http.Request, tx *sql.Tx, roomID, userID int64) (membership, error) {
	var member membership
	expected := r.Header.Get(membershipHeader)
	err := tx.QueryRowContext(r.Context(), `SELECT coalesce(r.owner_id,0),m.joined_after_message_id FROM members m JOIN rooms r ON r.id=m.room_id
		WHERE m.room_id=? AND m.user_id=? AND (?='' OR m.membership_id=?)`, roomID, userID, expected, expected).Scan(&member.ownerID, &member.joinedAfterMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return member, errMissing
	}
	return member, err
}

func roomError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, errMissing):
		fail(w, 404, "会话或成员不存在")
	case errors.Is(err, errOwner):
		fail(w, 403, "只有群主可管理成员，且不能移除群主")
	case errors.Is(err, errGroupFull):
		fail(w, 409, "群已达到 50 人")
	case errors.Is(err, errMember):
		fail(w, 409, "此用户已在群内")
	case errors.Is(err, errRequestConflict):
		fail(w, 409, "发送标识已被使用")
	default:
		internal(w, op, err)
	}
}

func audience(ctx context.Context, tx *sql.Tx, room int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT user_id FROM members WHERE room_id=?", room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	rid := intID(r.PathValue("room"))
	list := make([]user, 0)
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := access(r, tx, rid, u.ID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.username,u.display_name FROM members m
			JOIN users u ON u.id=m.user_id WHERE m.room_id=? ORDER BY u.username`, rid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var member user
			if err := rows.Scan(&member.ID, &member.Username, &member.DisplayName); err != nil {
				return err
			}
			list = append(list, member)
		}
		return rows.Err()
	})
	if err != nil {
		roomError(w, "list-members", err)
		return
	}
	reply(w, 200, list)
}

func (s *Server) addMember(w http.ResponseWriter, r *http.Request, u user) {
	var input struct {
		Username string `json:"username"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	ctx := r.Context()
	rid := intID(r.PathValue("room"))
	var people []int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := access(r, tx, rid, u.ID)
		if err != nil {
			return err
		}
		if current.ownerID != u.ID {
			return errOwner
		}
		member, err := findUser(ctx, tx, input.Username)
		if err != nil {
			return err
		}
		people, err = audience(ctx, tx, rid)
		if err != nil {
			return err
		}
		for _, id := range people {
			if id == member {
				return errMember
			}
		}
		if len(people) >= 50 {
			return errGroupFull
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO members(room_id,user_id,joined_after_message_id,membership_id)
			SELECT ?,?,coalesce(max(id),0),lower(hex(randomblob(16))) FROM messages WHERE room_id=?`, rid, member, rid)
		people = append(people, member)
		return err
	})
	if err != nil {
		roomError(w, "add-member", err)
		return
	}
	s.events.notify(people...)
	w.WriteHeader(204)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	rid, member := intID(r.PathValue("room")), intID(r.PathValue("member"))
	var people []int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := access(r, tx, rid, u.ID)
		if err != nil {
			return err
		}
		if current.ownerID != u.ID || member == current.ownerID {
			return errOwner
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM members WHERE room_id=? AND user_id=?", rid, member)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errMissing
		}
		people, err = audience(ctx, tx, rid)
		people = append(people, member)
		return err
	})
	if err != nil {
		roomError(w, "remove-member", err)
		return
	}
	s.events.notify(people...)
	w.WriteHeader(204)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	rid := intID(r.PathValue("room"))
	q := r.URL.Query()
	before, after := intID(q.Get("before")), intID(q.Get("after"))
	hasBefore, hasAfter := q.Has("before"), q.Has("after")
	if (hasBefore && before == 0) || (hasAfter && after == 0 && q.Get("after") != "0") || (hasBefore && hasAfter) {
		fail(w, 400, "游标无效")
		return
	}
	out := messagePage{Messages: make([]message, 0)}
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := access(r, tx, rid, u.ID)
		if err != nil {
			return err
		}
		base := `SELECT m.id,u.id,u.username,u.display_name,m.body,m.sent_at
			FROM messages m JOIN users u ON u.id=m.sender_id WHERE m.room_id=? AND m.id>?`
		var rs *sql.Rows
		switch {
		case hasBefore:
			rs, err = tx.QueryContext(ctx, base+" AND m.id<? ORDER BY m.id DESC LIMIT 51", rid, current.joinedAfterMessageID, before)
		case hasAfter:
			rs, err = tx.QueryContext(ctx, base+" ORDER BY m.id ASC LIMIT 51", rid, max(current.joinedAfterMessageID, after))
		default:
			rs, err = tx.QueryContext(ctx, base+" ORDER BY m.id DESC LIMIT 51", rid, current.joinedAfterMessageID)
		}
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var m message
			if err := rs.Scan(&m.ID, &m.Author.ID, &m.Author.Username, &m.Author.DisplayName, &m.Markdown, &m.SentAt); err != nil {
				return err
			}
			out.Messages = append(out.Messages, m)
		}
		if len(out.Messages) > 50 {
			out.More = true
			out.Messages = out.Messages[:50]
		}
		if !hasAfter {
			for l, h := 0, len(out.Messages)-1; l < h; l, h = l+1, h-1 {
				out.Messages[l], out.Messages[h] = out.Messages[h], out.Messages[l]
			}
		}
		return rs.Err()
	})
	if err != nil {
		roomError(w, "list-messages", err)
		return
	}
	reply(w, 200, out)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	rid := intID(r.PathValue("room"))
	var input struct {
		Markdown  string `json:"markdown"`
		RequestID string `json:"requestId"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Markdown) == "" || utf8.RuneCountInString(input.Markdown) > 8000 || len(input.RequestID) < 16 ||
		len(input.RequestID) > 64 || !validLabel(input.RequestID, 64) {
		fail(w, 400, "请输入最多 8000 字的消息，以及有效的发送标识")
		return
	}
	m := message{Author: u, Markdown: input.Markdown, SentAt: time.Now().UnixMilli()}
	var people []int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := access(r, tx, rid, u.ID)
		if err != nil {
			return err
		}
		var body string
		err = tx.QueryRowContext(ctx, `SELECT id,body,sent_at FROM messages WHERE room_id=? AND sender_id=? AND request_id=?`, rid, u.ID, input.RequestID).Scan(&m.ID, &body, &m.SentAt)
		if err == nil {
			if m.ID <= current.joinedAfterMessageID || body != input.Markdown {
				return errRequestConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO messages(room_id,sender_id,body,sent_at,request_id) VALUES (?,?,?,?,?)", rid, u.ID, m.Markdown, m.SentAt, input.RequestID)
		if err != nil {
			return err
		}
		m.ID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		people, err = audience(ctx, tx, rid)
		return err
	})
	if err != nil {
		roomError(w, "send-message", err)
		return
	}
	s.events.notify(people...)
	status := 200
	if len(people) > 0 {
		status = 201
	}
	reply(w, status, m)
}

func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	rid := intID(r.PathValue("room"))
	var people []int64
	err := transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := access(r, tx, rid, u.ID)
		if err != nil {
			return err
		}
		if current.ownerID == 0 || current.ownerID == u.ID {
			return errOwner
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM members WHERE room_id=? AND user_id=?", rid, u.ID); err != nil {
			return err
		}
		people, err = audience(ctx, tx, rid)
		return err
	})
	if errors.Is(err, errOwner) {
		fail(w, 403, "只有非群主成员可以退出群组")
		return
	}
	if err != nil {
		roomError(w, "leave-group", err)
		return
	}
	s.events.notify(append(people, u.ID)...)
	w.WriteHeader(204)
}
