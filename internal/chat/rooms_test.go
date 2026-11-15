package chat

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestConcurrentDirectRoom(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, alice := addUser(t, i.host.URL, "alice")
	b, bob := addUser(t, i.host.URL, "bob")
	peers := []struct {
		client *http.Client
		self   user
		peer   user
	}{
		{a, alice, bob},
		{b, bob, alice},
	}
	results := make([]struct{ ID int64 }, len(peers))
	t.Run("open_from_both_accounts", func(t *testing.T) {
		for index, p := range peers {
			t.Run(p.self.Username, func(t *testing.T) {
				t.Parallel()
				call(t, p.client, i.host.URL, "POST", "/rooms/direct", map[string]string{"username": p.peer.Username}, http.StatusOK, &results[index])
			})
		}
	})
	roomID := results[0].ID
	if roomID == 0 || roomID != results[1].ID {
		t.Fatalf("both accounts must open the same room: %+v", results)
	}
	path := fmt.Sprintf("/rooms/%d", roomID)
	for _, p := range peers {
		var rooms []room
		call(t, p.client, i.host.URL, "GET", "/rooms", nil, http.StatusOK, &rooms)
		if len(rooms) != 1 || rooms[0].ID != roomID || rooms[0].Kind != "direct" || rooms[0].PeerUsername != p.peer.Username || rooms[0].MemberCount != 2 {
			t.Fatalf("%s rooms: %+v", p.self.Username, rooms)
		}
		var members []user
		call(t, p.client, i.host.URL, "GET", path+"/members", nil, http.StatusOK, &members)
		if !slices.Equal(members, []user{alice, bob}) {
			t.Fatalf("%s direct room members: %+v", p.self.Username, members)
		}
	}
	call(t, a, i.host.URL, "POST", path+"/members", map[string]string{"username": "bob"}, http.StatusForbidden)
	call(t, a, i.host.URL, "DELETE", fmt.Sprintf("%s/members/%d", path, bob.ID), nil, http.StatusForbidden)
	call(t, b, i.host.URL, "POST", path+"/leave", nil, http.StatusForbidden)
}

func TestGroupMembershipPermissions(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, alice := addUser(t, i.host.URL, "alice")
	b, bob := addUser(t, i.host.URL, "bob")
	c, carol := addUser(t, i.host.URL, "carol")
	origin := i.host.URL
	var created struct{ ID int64 }
	call(t, a, origin, "POST", "/rooms/group", map[string]any{"name": "小群", "usernames": []string{"bob"}}, http.StatusCreated, &created)
	path := fmt.Sprintf("/rooms/%d", created.ID)
	removeCarol := fmt.Sprintf("%s/members/%d", path, carol.ID)
	addCarol := map[string]string{"username": "  CAROL  "}
	message := map[string]string{"markdown": "合成群消息", "requestId": "fixture_group_message"}

	checkMembers := func(client *http.Client, want ...user) {
		t.Helper()
		var members []user
		call(t, client, origin, "GET", path+"/members", nil, http.StatusOK, &members)
		if !slices.Equal(members, want) {
			t.Fatalf("members %+v, want %+v", members, want)
		}
	}
	checkDenied := func(client *http.Client) {
		t.Helper()
		var rooms []room
		call(t, client, origin, "GET", "/rooms", nil, http.StatusOK, &rooms)
		if len(rooms) != 0 {
			t.Fatalf("nonmember can list rooms: %+v", rooms)
		}
		call(t, client, origin, "GET", path+"/members", nil, http.StatusNotFound)
		call(t, client, origin, "GET", path+"/messages", nil, http.StatusNotFound)
		call(t, client, origin, "GET", path+"/messages?after=1", nil, http.StatusNotFound)
		call(t, client, origin, "POST", path+"/messages", message, http.StatusNotFound)
	}
	checkMembers(a, alice, bob)
	checkDenied(c)
	call(t, c, origin, "POST", path+"/members", addCarol, http.StatusNotFound)
	call(t, c, origin, "DELETE", removeCarol, nil, http.StatusNotFound)
	call(t, c, origin, "POST", path+"/leave", nil, http.StatusNotFound)
	call(t, b, origin, "POST", path+"/members", addCarol, http.StatusForbidden)
	call(t, b, origin, "DELETE", removeCarol, nil, http.StatusForbidden)
	call(t, a, origin, "POST", path+"/leave", nil, http.StatusForbidden)
	call(t, a, origin, "DELETE", fmt.Sprintf("%s/members/%d", path, alice.ID), nil, http.StatusForbidden)
	call(t, a, origin, "POST", path+"/members", map[string]string{"username": "car"}, http.StatusNotFound)
	checkMembers(a, alice, bob)

	call(t, a, origin, "POST", path+"/members", addCarol, http.StatusNoContent)
	call(t, a, origin, "POST", path+"/members", addCarol, http.StatusConflict)
	checkMembers(c, alice, bob, carol)
	var rooms []room
	call(t, c, origin, "GET", "/rooms", nil, http.StatusOK, &rooms)
	if len(rooms) != 1 || rooms[0].ID != created.ID || rooms[0].OwnerID != alice.ID || rooms[0].MemberCount != 3 {
		t.Fatalf("joined member sees unexpected rooms: %+v", rooms)
	}
	call(t, c, origin, "POST", path+"/messages", message, http.StatusCreated)
	call(t, c, origin, "POST", path+"/leave", nil, http.StatusNoContent)
	checkDenied(c)
	checkMembers(b, alice, bob)

	call(t, a, origin, "POST", path+"/members", addCarol, http.StatusNoContent)
	checkMembers(c, alice, bob, carol)
	call(t, a, origin, "DELETE", removeCarol, nil, http.StatusNoContent)
	checkDenied(c)
	call(t, a, origin, "DELETE", removeCarol, nil, http.StatusNotFound)
	checkMembers(b, alice, bob)
	call(t, c, origin, "GET", "/me", nil, http.StatusOK)

	other := newInstance(t, Config{Registration: RegistrationOpen})
	foreign, sameName := addUser(t, other.host.URL, "alice")
	if sameName.ID != alice.ID {
		t.Fatal("fixture needs matching numeric account IDs across independent instances")
	}
	response := call(t, foreign, origin, "GET", path+"/members", nil, http.StatusUnauthorized)
	if _, err := response.Request.Cookie(sessionCookie); err != nil {
		t.Fatal("the request must carry the other instance's session cookie")
	}
	call(t, foreign, origin, "GET", "/rooms", nil, http.StatusUnauthorized)
	call(t, foreign, origin, "GET", path+"/messages", nil, http.StatusUnauthorized)
	call(t, foreign, origin, "POST", path+"/messages", message, http.StatusUnauthorized)
	call(t, foreign, origin, "POST", path+"/members", addCarol, http.StatusUnauthorized)
}

func TestGroupHistoryUsesCurrentMembership(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	c, _ := addUser(t, i.host.URL, "carol")
	origin := i.host.URL
	var group struct{ ID int64 }
	call(t, a, origin, "POST", "/rooms/group", map[string]string{"name": "加入边界"}, http.StatusCreated, &group)
	roomPath := fmt.Sprintf("/rooms/%d", group.ID)
	messages := roomPath + "/messages"
	join := func() {
		t.Helper()
		call(t, a, origin, "POST", roomPath+"/members", map[string]string{"username": "carol"}, http.StatusNoContent)
	}
	send := func(client *http.Client, key string) int64 {
		t.Helper()
		var posted message
		call(t, client, origin, "POST", messages, map[string]string{"markdown": key, "requestId": key}, http.StatusCreated, &posted)
		return posted.ID
	}
	checkPage := func(client *http.Client, query string, ids ...int64) {
		t.Helper()
		var page messagePage
		call(t, client, origin, "GET", messages+query, nil, http.StatusOK, &page)
		var got []int64
		for _, m := range page.Messages {
			got = append(got, m.ID)
		}
		if !slices.Equal(got, ids) || page.More {
			t.Fatalf("page %q: got %v and more=%v, want %v", query, got, page.More, ids)
		}
	}
	checkLast := func(want int64) {
		t.Helper()
		var rooms []room
		call(t, c, origin, "GET", "/rooms", nil, http.StatusOK, &rooms)
		if len(rooms) != 1 || rooms[0].LastMessageID != want {
			t.Fatalf("rooms: %+v, want last visible message %d", rooms, want)
		}
	}

	before := send(a, "fixture_before_join")
	join()
	checkPage(c, "")
	checkLast(0)
	const firstRequest = "fixture_first_membership"
	first := send(c, firstRequest)
	checkPage(c, "", first)
	checkLast(first)
	retry := map[string]string{"markdown": firstRequest, "requestId": firstRequest}
	var same message
	call(t, c, origin, "POST", messages, retry, http.StatusOK, &same)
	if same.ID != first {
		t.Fatalf("retry returned %d, want %d", same.ID, first)
	}

	call(t, c, origin, "POST", roomPath+"/leave", nil, http.StatusNoContent)
	gap := send(a, "fixture_between_memberships")
	call(t, c, origin, "GET", messages, nil, http.StatusNotFound)
	join()
	checkLast(0)
	checkPage(c, fmt.Sprintf("?after=%d", first))
	var conflict map[string]string
	call(t, c, origin, "POST", messages, retry, http.StatusConflict, &conflict)
	if len(conflict) != 1 || conflict["error"] == "" {
		t.Fatalf("retry from previous membership returned message data: %+v", conflict)
	}
	newFirst := send(a, "fixture_after_rejoin")
	newSecond := send(c, "fixture_new_membership")
	checkLast(newSecond)
	checkPage(c, "", newFirst, newSecond)
	checkPage(c, fmt.Sprintf("?after=%d", first), newFirst, newSecond)
	checkPage(c, fmt.Sprintf("?after=%d", newFirst), newSecond)
	checkPage(c, fmt.Sprintf("?before=%d", newSecond), newFirst)
	checkPage(c, fmt.Sprintf("?before=%d", gap))
	checkPage(a, "", before, first, gap, newFirst, newSecond)
}

func TestGroupJoinAndSendUseServerOrder(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	a, _ := addUser(t, i.host.URL, "alice")
	c, carol := addUser(t, i.host.URL, "carol")
	origin := i.host.URL
	for n := range 5 {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var group struct{ ID int64 }
			call(t, a, origin, "POST", "/rooms/group", map[string]string{"name": "并行加入"}, http.StatusCreated, &group)
			path := fmt.Sprintf("/rooms/%d", group.ID)
			body := func(key string) map[string]string { return map[string]string{"markdown": key, "requestId": key} }
			var prior, concurrent, later message
			call(t, a, origin, "POST", path+"/messages", body("fixture_before_race"), http.StatusCreated, &prior)
			t.Run("concurrent_requests", func(t *testing.T) {
				t.Run("join", func(t *testing.T) {
					t.Parallel()
					call(t, a, origin, "POST", path+"/members", map[string]string{"username": "carol"}, http.StatusNoContent)
				})
				t.Run("send", func(t *testing.T) {
					t.Parallel()
					call(t, a, origin, "POST", path+"/messages", body("fixture_during_race"), http.StatusCreated, &concurrent)
				})
			})
			call(t, a, origin, "POST", path+"/messages", body("fixture_after_race"), http.StatusCreated, &later)
			var boundary int64
			err := i.s.db.QueryRow("SELECT joined_after_message_id FROM members WHERE room_id=? AND user_id=?", group.ID, carol.ID).Scan(&boundary)
			if err != nil || (boundary != prior.ID && boundary != concurrent.ID) {
				t.Fatalf("unexpected boundary %d: %v", boundary, err)
			}
			want := []message{later}
			if boundary == prior.ID {
				want = []message{concurrent, later}
			}
			var page messagePage
			call(t, c, origin, "GET", path+"/messages?after="+fmt.Sprint(prior.ID), nil, http.StatusOK, &page)
			if !slices.Equal(page.Messages, want) || page.More {
				t.Fatalf("boundary %d: got %+v, want %+v", boundary, page, want)
			}
		})
	}
}
