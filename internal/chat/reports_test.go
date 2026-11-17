package chat

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reportEndpoint(roomID, messageID int64) string {
	return fmt.Sprintf("/rooms/%d/messages/%d/reports", roomID, messageID)
}

func sendReportFixtureMessage(t *testing.T, origin string, client *http.Client, roomID int64, body string) int64 {
	t.Helper()
	var saved struct{ ID int64 }
	call(t, client, origin, "POST", fmt.Sprintf("/rooms/%d/messages", roomID),
		map[string]string{"markdown": body, "requestId": randomText(18)}, 201, &saved)
	return saved.ID
}

func submitReportFixture(t *testing.T, origin string, client *http.Client, roomID, messageID int64, reason string, status int) int64 {
	t.Helper()
	input := map[string]string{"reason": reason}
	path := reportEndpoint(roomID, messageID)
	if status != 200 && status != 201 {
		response := call(t, client, origin, "POST", path, input, status)
		if status == 429 && response.Header.Get("Retry-After") != "60" {
			t.Fatal("report limit omitted Retry-After")
		}
		return 0
	}
	var record struct{ ID int64 }
	call(t, client, origin, "POST", path, input, status, &record)
	if record.ID <= 0 {
		t.Fatal("report receipt omitted a valid ID")
	}
	return record.ID
}

func TestGroupReportVisibilityAndDuplicates(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin := i.host.URL
	owner, ownerAccount := addUser(t, origin, "owner")
	member, account := addUser(t, origin, "member")
	group := createTestGroup(t, owner, origin, "First group")
	otherGroup := createTestGroup(t, owner, origin, "Other group", account.Username)
	old := sendReportFixtureMessage(t, origin, owner, group, "Before member joined")
	memberPath := fmt.Sprintf("/rooms/%d/members", group)
	call(t, owner, origin, "POST", memberPath, map[string]string{"username": account.Username}, 204)
	current := sendReportFixtureMessage(t, origin, owner, group, "Visible report target")
	call(t, browser(t), origin, "POST", reportEndpoint(group, current), map[string]string{"reason": "spam"}, 401)
	submitReportFixture(t, origin, member, group, old, "spam", 404)

	request, err := http.NewRequest("POST", origin+"/api/v1"+reportEndpoint(group, current), strings.NewReader(`{"reason":"spam"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(membershipHeader, "old-membership")
	res, err := member.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("wrong membership got %d", res.StatusCode)
	}
	first := submitReportFixture(t, origin, member, group, current, "spam", 201)
	if repeated := submitReportFixture(t, origin, member, group, current, "spam", 200); repeated != first {
		t.Fatal("same reporter and reason changed the receipt")
	}
	submitReportFixture(t, origin, member, group, current, "other", 409)
	submitReportFixture(t, origin, member, otherGroup, current, "spam", 404)
	var direct struct{ ID int64 }
	call(t, owner, origin, "POST", "/rooms/direct", map[string]string{"username": account.Username}, 200, &direct)
	directMessage := sendReportFixtureMessage(t, origin, owner, direct.ID, "Direct message")
	submitReportFixture(t, origin, member, direct.ID, directMessage, "spam", 404)
	call(t, owner, origin, "DELETE", fmt.Sprintf("%s/%d", memberPath, account.ID), nil, 204)
	submitReportFixture(t, origin, member, group, current, "spam", 404)
	call(t, owner, origin, "POST", memberPath, map[string]string{"username": account.Username}, 204)
	submitReportFixture(t, origin, member, group, current, "spam", 404)
	submitReportFixture(t, origin, member, group, current, "spam", 404)
	submitReportFixture(t, origin, member, group, current, "spam", 429)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM message_reports", 1)
	if _, err := i.s.db.Exec("DELETE FROM messages WHERE id=?", current); err != nil {
		t.Fatal(err)
	}
	if _, err := i.s.db.Exec(`INSERT INTO messages(id,room_id,sender_id,body,sent_at,request_id)
		VALUES (?,?,?,'Unrelated replacement',0,'fixture-replaced')`, current, group, ownerAccount.ID); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	review, err := ReviewMessageReport(ctx, i.cfg.Database, first)
	if err != nil || review.Evidence != nil || review.Report.MessageID != 0 {
		t.Fatalf("missing evidence review: %+v %v", review, err)
	}
	if _, err := CloseMessageReport(ctx, i.cfg.Database, first, ReportAccountDisabled); err == nil {
		t.Fatal("case with missing evidence suspended an account")
	}
	if closed, err := CloseMessageReport(ctx, i.cfg.Database, first, ReportDismissed); err != nil || closed.Decision != ReportDismissed {
		t.Fatalf("dismissed missing evidence: %+v %v", closed, err)
	}
}

func TestReportReviewAndAtomicSuspension(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin, database := i.host.URL, i.cfg.Database
	author, person := addUser(t, origin, "author")
	reporter, member := addUser(t, origin, "reporter")
	group := createTestGroup(t, author, origin, "Report group", member.Username)
	messageID := sendReportFixtureMessage(t, origin, author, group, "Synthetic report evidence")
	reportID := submitReportFixture(t, origin, reporter, group, messageID, "harassment", 201)
	call(t, author, origin, "GET", "/ops/reports", nil, 404)
	call(t, reporter, origin, "GET", "/ops/reports", nil, 404)
	bot := createTestBot(t, author, origin, "Fixture Bot")
	token := rotateTestBot(t, author, origin, bot.ID)
	var installation botInstallationPreview
	call(t, author, origin, "PUT", fmt.Sprintf("/rooms/%d/bots/%s/installation", group, bot.ID),
		map[string][]string{"permissions": (botCommandPermissions{receive: true, reply: true}).list()}, 200, &installation)
	event := invoke(t, reporter, origin, commandPath(group, installation.InstallationID), "ping", "report-fixture-command", 202)
	var reply replyWireMessage
	postBotReply(t, reporter, origin, token, group, event, "Synthetic Bot evidence", 201, &reply, installation.InstallationID)
	botReport := submitReportFixture(t, origin, reporter, group, reply.ID, "spam", 201)
	ctx := t.Context()
	if _, err := CloseMessageReport(ctx, database, botReport, ReportAccountDisabled); err == nil {
		t.Fatal("Bot report allowed suspending the owner")
	}
	if closed, err := CloseMessageReport(ctx, database, botReport, ReportReferred); err != nil || closed.Decision != ReportReferred {
		t.Fatalf("Bot referral = %+v, error %v", closed, err)
	}
	checkBotToken(t, origin, token, 200, nil)

	if _, err := i.s.db.Exec(`CREATE TRIGGER refuse_review BEFORE INSERT ON maintenance_audit WHEN NEW.action='report.review'
		BEGIN SELECT RAISE(ABORT,'review audit failed'); END`); err != nil {
		t.Fatal(err)
	}
	if review, err := ReviewMessageReport(ctx, database, reportID); err == nil || review.Evidence != nil {
		t.Fatal("review returned evidence when audit failed")
	}
	if _, err := i.s.db.Exec("DROP TRIGGER refuse_review"); err != nil {
		t.Fatal(err)
	}
	review, err := ReviewMessageReport(ctx, database, reportID)
	if err != nil || review.Evidence == nil || review.Evidence.Markdown != "Synthetic report evidence" || review.Evidence.AuthorUserID != person.ID || review.Report.Status != "open" {
		t.Fatalf("unexpected review: %+v, %v", review, err)
	}
	if _, err := i.s.db.Exec(`CREATE TRIGGER refuse_close BEFORE INSERT ON maintenance_audit WHEN NEW.action='report.close'
		BEGIN SELECT RAISE(ABORT,'close audit failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseMessageReport(ctx, database, reportID, ReportAccountDisabled); err == nil {
		t.Fatal("case closed even though final audit failed")
	}
	call(t, author, origin, "GET", "/me", nil, 200)
	testBotRoomAccess(t, origin, token, group, installation.InstallationID, 200)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit WHERE action='account.disable'", 0)
	open, err := ListMessageReports(ctx, database, "open", 0, 1)
	if err != nil || len(open) != 1 || open[0].ID != reportID {
		t.Fatalf("report did not stay open: %+v %v", open, err)
	}
	if _, err := i.s.db.Exec("DROP TRIGGER refuse_close"); err != nil {
		t.Fatal(err)
	}
	stream, stop := startStream(t, origin, author)
	defer stop()
	readSync(t, stream)
	closed, err := CloseMessageReport(ctx, database, reportID, ReportAccountDisabled)
	if err != nil || closed.Decision != ReportAccountDisabled || closed.ClosedAt == 0 {
		t.Fatalf("suspended case = %+v, error %v", closed, err)
	}
	if repeat, err := CloseMessageReport(ctx, database, reportID, ReportAccountDisabled); err != nil || repeat != closed {
		t.Fatalf("retry changed decision: %+v %v", repeat, err)
	}
	if _, err := CloseMessageReport(ctx, database, reportID, ReportDismissed); err == nil {
		t.Fatal("conflicting close overwrote decision")
	}
	call(t, author, origin, "GET", "/me", nil, 401)
	checkBotToken(t, origin, token, 401, nil)
	sendReportFixtureMessage(t, origin, reporter, group, "Reporter still has access")
	if tail, err := io.ReadAll(stream); err != nil || string(tail) != "data: {}\n\n" {
		t.Fatalf("SSE did not close on the next notification: %q %v", tail, err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit WHERE action='account.disable' AND report_id IS NOT NULL", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit WHERE action='report.close'", 2)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM bot_installations", 0)
	backup := filepath.Join(t.TempDir(), "report-snapshot.sqlite")
	if err := CopyDatabase(ctx, database, backup); err != nil {
		t.Fatal(err)
	}
	if page, err := ListMessageReports(ctx, backup, "closed", 0, 1); err != nil || len(page) != 1 || page[0].ID != botReport {
		t.Fatalf("restored snapshot closed page: %+v %v", page, err)
	}
	if _, err := i.s.db.Exec("UPDATE message_reports SET closed_at=? WHERE id=?", time.Now().Add(-31*24*time.Hour).UnixMilli(), botReport); err != nil {
		t.Fatal(err)
	}
	if page, err := ListMessageReports(ctx, database, "closed", 0, 50); err != nil || len(page) != 1 || page[0] != closed {
		t.Fatalf("closed report retention window: %+v %v", page, err)
	}
}
