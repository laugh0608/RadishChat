package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func createTestGroup(t *testing.T, client *http.Client, origin, name string, members ...string) int64 {
	t.Helper()
	var created struct{ ID int64 }
	call(t, client, origin, http.MethodPost, "/rooms/group",
		map[string]any{"name": name, "usernames": members}, http.StatusCreated, &created)
	return created.ID
}

func installTestBot(t *testing.T, client *http.Client, origin, path string) botInstallationPreview {
	t.Helper()
	var result botInstallationPreview
	call(t, client, origin, http.MethodPut, path, map[string][]string{"permissions": {botInstallationRead}}, http.StatusOK, &result)
	if result.InstallationID == "" || !slices.Equal(result.Permissions, []string{botInstallationRead}) {
		t.Fatal("installation response omitted the grant")
	}
	return result
}

func testBotRoomAccess(t *testing.T, origin, token string, roomID int64, expectedID string, wantStatus int) botRoomGrant {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf("%s/api/v1/bot/rooms/%d/installation", origin, roomID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if expectedID != "" {
		req.Header.Set(botInstallationHeader, expectedID)
	}
	res, err := browser(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != wantStatus {
		t.Fatalf("GET installation for room %d: status %d, want %d", roomID, res.StatusCode, wantStatus)
	}
	if wantStatus == http.StatusUnauthorized && !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer ") {
		t.Fatal("missing Bearer challenge")
	}
	var grant botRoomGrant
	if wantStatus == http.StatusOK {
		var response json.RawMessage
		if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(response, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 || fields["botId"] == nil || fields["roomId"] == nil || fields["installationId"] == nil || fields["permissions"] == nil {
			t.Fatal("Bot API exposed fields outside the installation grant")
		}
		if err := json.Unmarshal(response, &grant); err != nil {
			t.Fatal(err)
		}
	}
	return grant
}

func TestGroupBotInstallationAuthorizationAndLifecycle(t *testing.T) {
	instance := newInstance(t, Config{Registration: RegistrationOpen})
	origin := instance.host.URL
	groupOwner, _ := addUser(t, origin, "group-owner")
	developer, _ := addUser(t, origin, "developer")
	member, person := addUser(t, origin, "member")
	record := createTestBot(t, developer, origin, "Fixture Bot")
	token := rotateTestBot(t, developer, origin, record.ID)
	group := createTestGroup(t, groupOwner, origin, "First group", person.Username)
	otherGroup := createTestGroup(t, groupOwner, origin, "Second group")
	var direct struct{ ID int64 }
	call(t, groupOwner, origin, http.MethodPost, "/rooms/direct", map[string]string{"username": person.Username}, http.StatusOK, &direct)

	groupPath := fmt.Sprintf("/rooms/%d", group)
	listPath := groupPath + "/bot-installations"
	installPath := groupPath + "/bots/" + record.ID + "/installation"
	ownedPath := "/bots/" + record.ID + "/installations"
	approvePath := func(id string) string { return ownedPath + "/" + id + "/approve" }
	removePath := func(id string) string { return "/bot-installations/" + id }
	approval := map[string][]string{"permissions": {botInstallationRead}}
	step := func(c *http.Client, method, path string, body any, want int) {
		t.Helper()
		call(t, c, origin, method, path, body, want)
	}

	var preview botInstallationPreview
	call(t, groupOwner, origin, http.MethodGet, installPath, nil, http.StatusOK, &preview)
	if preview.InstallationID != "" || preview.State != "available" || preview.BotID != record.ID || preview.DisplayName != record.DisplayName ||
		!slices.Equal(preview.Permissions, []string{botInstallationRead}) {
		t.Fatal("unexpected installation preview")
	}
	step(member, http.MethodGet, installPath, nil, http.StatusForbidden)
	step(member, http.MethodPut, installPath, approval, http.StatusForbidden)
	step(developer, http.MethodPut, installPath, approval, http.StatusNotFound)
	step(developer, http.MethodGet, listPath, nil, http.StatusNotFound)
	step(groupOwner, http.MethodGet, fmt.Sprintf("/rooms/%d/bot-installations", direct.ID), nil, http.StatusNotFound)
	step(groupOwner, http.MethodPut, fmt.Sprintf("/rooms/%d/bots/%s/installation", direct.ID, record.ID), approval, http.StatusNotFound)
	for _, input := range []any{map[string]any{}, map[string][]string{"permissions": {"messages:read"}}, map[string][]string{"permissions": {botInstallationRead, botInstallationRead}}} {
		step(groupOwner, http.MethodPut, installPath, input, http.StatusBadRequest)
	}

	pending := installTestBot(t, groupOwner, origin, installPath)
	if retry := installTestBot(t, groupOwner, origin, installPath); retry.InstallationID != pending.InstallationID ||
		retry.State != "pending" || pending.State != "pending" {
		t.Fatal("retry changed the pending installation")
	}
	var groupList []groupBotInstallation
	call(t, member, origin, http.MethodGet, listPath, nil, http.StatusOK, &groupList)
	if len(groupList) != 0 {
		t.Fatal("pending installation was visible to an ordinary member")
	}
	step(member, http.MethodGet, listPath+"?includePending=1", nil, http.StatusForbidden)
	var pendingList []botInstallationPreview
	call(t, groupOwner, origin, http.MethodGet, listPath+"?includePending=1", nil, http.StatusOK, &pendingList)
	if len(pendingList) != 1 || pendingList[0].InstallationID != pending.InstallationID || pendingList[0].State != "pending" {
		t.Fatal("group owner could not recover the pending installation from the group list")
	}
	var ownerList []ownedBotInstallation
	call(t, developer, origin, http.MethodGet, ownedPath, nil, http.StatusOK, &ownerList)
	if len(ownerList) != 1 || ownerList[0].InstallationID != pending.InstallationID || ownerList[0].State != "pending" ||
		ownerList[0].RoomID != group || ownerList[0].RequestedBy != "group-owner" {
		t.Fatal("Bot owner did not receive the pending request")
	}
	step(member, http.MethodDelete, removePath(pending.InstallationID), nil, http.StatusNotFound)
	step(groupOwner, http.MethodGet, ownedPath, nil, http.StatusNotFound)
	step(groupOwner, http.MethodPost, approvePath(pending.InstallationID), nil, http.StatusNotFound)
	testBotRoomAccess(t, origin, token, group, "", http.StatusNotFound)
	step(developer, http.MethodPost, approvePath(pending.InstallationID), nil, http.StatusNoContent)
	step(developer, http.MethodPost, approvePath(pending.InstallationID), nil, http.StatusNoContent)
	call(t, groupOwner, origin, http.MethodGet, listPath+"?includePending=1", nil, http.StatusOK, &pendingList)
	if len(pendingList) != 1 || pendingList[0].State != "active" {
		t.Fatal("group owner's list did not reflect approval")
	}

	call(t, member, origin, http.MethodGet, listPath, nil, http.StatusOK, &groupList)
	if len(groupList) != 1 || groupList[0].InstallationID != pending.InstallationID || groupList[0].BotID != record.ID ||
		groupList[0].DisplayName != record.DisplayName || !slices.Equal(groupList[0].Permissions, []string{botInstallationRead}) {
		t.Fatal("group member could not inspect the active grant")
	}
	grant := testBotRoomAccess(t, origin, token, group, pending.InstallationID, http.StatusOK)
	if grant.RoomID != group || grant.BotID != record.ID || grant.InstallationID != pending.InstallationID ||
		!slices.Equal(grant.Permissions, []string{botInstallationRead}) {
		t.Fatal("unexpected grant")
	}
	testBotRoomAccess(t, origin, token, otherGroup, "", http.StatusNotFound)
	testBotRoomAccess(t, origin, token, direct.ID, "", http.StatusNotFound)
	testBotRoomAccess(t, origin, "invalid", group, "", http.StatusUnauthorized)
	step(member, http.MethodDelete, removePath(pending.InstallationID), nil, http.StatusForbidden)
	botRequest(t, browser(t), origin, http.MethodGet, groupPath+"/messages", []string{"Bearer " + token}, http.StatusUnauthorized, nil)
	botRequest(t, browser(t), origin, http.MethodGet, groupPath+"/members", []string{"Bearer " + token}, http.StatusUnauthorized, nil)

	approvedOther := installTestBot(t, groupOwner, origin, fmt.Sprintf("/rooms/%d/bots/%s/installation", otherGroup, record.ID))
	step(developer, http.MethodPost, approvePath(approvedOther.InstallationID), nil, http.StatusNoContent)
	step(groupOwner, http.MethodDelete, removePath(pending.InstallationID), nil, http.StatusNoContent)
	testBotRoomAccess(t, origin, token, group, "", http.StatusNotFound)
	testBotRoomAccess(t, origin, token, otherGroup, approvedOther.InstallationID, http.StatusOK)
	fresh := installTestBot(t, groupOwner, origin, installPath)
	if fresh.InstallationID == pending.InstallationID || fresh.State != "pending" {
		t.Fatal("reinstallation reused a revoked authorization")
	}
	step(developer, http.MethodPost, approvePath(pending.InstallationID), nil, http.StatusNotFound)
	step(groupOwner, http.MethodDelete, removePath(pending.InstallationID), nil, http.StatusNotFound)
	step(developer, http.MethodPost, approvePath(fresh.InstallationID), nil, http.StatusNoContent)
	testBotRoomAccess(t, origin, token, group, pending.InstallationID, http.StatusNotFound)
	testBotRoomAccess(t, origin, token, group, fresh.InstallationID, http.StatusOK)

	newToken := rotateTestBot(t, developer, origin, record.ID)
	testBotRoomAccess(t, origin, token, group, fresh.InstallationID, http.StatusUnauthorized)
	testBotRoomAccess(t, origin, newToken, group, fresh.InstallationID, http.StatusOK)
	step(developer, http.MethodDelete, removePath(fresh.InstallationID), nil, http.StatusNoContent)
	testBotRoomAccess(t, origin, newToken, group, "", http.StatusNotFound)
	testBotRoomAccess(t, origin, newToken, otherGroup, approvedOther.InstallationID, http.StatusOK)
	step(groupOwner, http.MethodDelete, fmt.Sprintf("%s/members/%d", groupPath, person.ID), nil, http.StatusNoContent)
	step(member, http.MethodGet, listPath, nil, http.StatusNotFound)

	selfOwned := createTestBot(t, groupOwner, origin, "Self-owned")
	selfToken := rotateTestBot(t, groupOwner, origin, selfOwned.ID)
	immediate := installTestBot(t, groupOwner, origin, groupPath+"/bots/"+selfOwned.ID+"/installation")
	if immediate.State != "active" {
		t.Fatal("same owner installation should take effect immediately")
	}
	testBotRoomAccess(t, origin, selfToken, group, immediate.InstallationID, http.StatusOK)
}

func TestBotInstallationApprovalTracksCurrentGroupOwner(t *testing.T) {
	instance := newInstance(t, Config{Registration: RegistrationOpen})
	origin := instance.host.URL
	owner, _ := addUser(t, origin, "group-owner")
	developer, _ := addUser(t, origin, "developer")
	successor, member := addUser(t, origin, "successor")
	record := createTestBot(t, developer, origin, "Fixture Bot")
	token := rotateTestBot(t, developer, origin, record.ID)
	group := createTestGroup(t, owner, origin, "Test group", member.Username)
	path := fmt.Sprintf("/rooms/%d/bots/%s/installation", group, record.ID)
	old := installTestBot(t, owner, origin, path)
	if _, err := instance.s.db.Exec("UPDATE rooms SET owner_id=? WHERE id=?", member.ID, group); err != nil {
		t.Fatal(err)
	}
	call(t, developer, origin, http.MethodPost, "/bots/"+record.ID+"/installations/"+old.InstallationID+"/approve", nil, http.StatusNotFound)
	testBotRoomAccess(t, origin, token, group, "", http.StatusNotFound)
	call(t, successor, origin, http.MethodDelete, "/bot-installations/"+old.InstallationID, nil, http.StatusNoContent)
	fresh := installTestBot(t, successor, origin, path)
	call(t, developer, origin, http.MethodPost, "/bots/"+record.ID+"/installations/"+fresh.InstallationID+"/approve", nil, http.StatusNoContent)
	testBotRoomAccess(t, origin, token, group, fresh.InstallationID, http.StatusOK)
}

func TestBotInstallationConcurrentApprovalAndRemoval(t *testing.T) {
	instance := newInstance(t, Config{Registration: RegistrationOpen})
	origin := instance.host.URL
	groupOwner, _ := addUser(t, origin, "group-owner")
	developer, _ := addUser(t, origin, "developer")
	record := createTestBot(t, developer, origin, "Fixture Bot")
	token := rotateTestBot(t, developer, origin, record.ID)
	group := createTestGroup(t, groupOwner, origin, "Test group")
	path := fmt.Sprintf("/rooms/%d/bots/%s/installation", group, record.ID)
	var ids [2]string
	t.Run("concurrent puts", func(t *testing.T) {
		for i, name := range []string{"first", "second"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ids[i] = installTestBot(t, groupOwner, origin, path).InstallationID
			})
		}
	})
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatal("simultaneous requests did not resolve to one pending installation")
	}
	remove := "/bot-installations/" + ids[0]
	approve := "/bots/" + record.ID + "/installations/" + ids[0] + "/approve"
	t.Run("competing mutations", func(t *testing.T) {
		t.Run("approve", func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+"/api/v1"+approve, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", origin)
			res, err := developer.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusNotFound {
				t.Fatalf("approval returned %d", res.StatusCode)
			}
		})
		t.Run("remove", func(t *testing.T) {
			t.Parallel()
			call(t, groupOwner, origin, http.MethodDelete, remove, nil, http.StatusNoContent)
		})
	})
	testBotRoomAccess(t, origin, token, group, ids[0], http.StatusNotFound)
	checkDBInt(t, instance.s.db, "SELECT count(*) FROM bot_installations", 0)
	call(t, developer, origin, http.MethodPost, approve, nil, http.StatusNotFound)
}
