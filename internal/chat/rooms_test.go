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
