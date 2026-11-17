package chat

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type subscribers struct {
	mu    sync.Mutex
	users map[int64]map[chan struct{}]struct{}
	total int
}

func (s *subscribers) subscribe(id int64) (chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total >= 512 || len(s.users[id]) >= 8 {
		return nil, func() {}
	}
	if s.users == nil {
		s.users = make(map[int64]map[chan struct{}]struct{})
	}
	if s.users[id] == nil {
		s.users[id] = make(map[chan struct{}]struct{})
	}
	ch := make(chan struct{}, 1)
	s.users[id][ch] = struct{}{}
	s.total++
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.users[id], ch)
		if len(s.users[id]) == 0 {
			delete(s.users, id)
		}
		s.total--
	}
}

func (s *subscribers) notify(ids ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		for ch := range s.users[id] {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, u user) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "请求来源不匹配")
		return
	}
	ch, stop := s.events.subscribe(u.ID)
	defer stop()
	if ch == nil {
		fail(w, 429, "实时连接过多")
		return
	}
	token := s.sessionToken(r)
	if _, err := sessionIdentity(r.Context(), s.db, token); err != nil {
		userError(w, "start-stream", err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	write := func(v string) bool {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(w, v); err != nil || control.Flush() != nil {
			return false
		}
		_ = control.SetWriteDeadline(time.Time{})
		return true
	}
	if !write("retry: 2500\nevent: sync\ndata: {}\n\n") {
		return
	}
	check := time.NewTicker(15 * time.Second)
	defer check.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := sessionIdentity(r.Context(), s.db, token); err != nil || !write("event: sync\ndata: {}\n\n") {
				return
			}
		case <-check.C:
			if _, err := sessionIdentity(r.Context(), s.db, token); err != nil || !write(": keepalive\n\n") {
				return
			}
		}
	}
}
