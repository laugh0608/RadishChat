package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGovernanceDataExportAndRetention(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin, database := i.host.URL, i.cfg.Database
	owner, requester := addUser(t, origin, "requester")
	peer, other := addUser(t, origin, "other")
	room := createTestGroup(t, owner, origin, "Private group", other.Username)
	const evidence = "Fixture private message正文"
	first := sendReportFixtureMessage(t, origin, peer, room, evidence)
	second := sendReportFixtureMessage(t, origin, peer, room, "Recent closed target")
	third := sendReportFixtureMessage(t, origin, peer, room, "Old closed target")
	foreignMessage := sendReportFixtureMessage(t, origin, owner, room, "Other user's target")
	pending := submitReportFixture(t, origin, owner, room, first, "spam", 201)
	recent := submitReportFixture(t, origin, owner, room, second, "other", 201)
	expired := submitReportFixture(t, origin, owner, room, third, "harassment", 201)
	submitReportFixture(t, origin, peer, room, foreignMessage, "spam", 201)
	for _, report := range []int64{recent, expired} {
		if _, err := CloseMessageReport(t.Context(), database, report, ReportDismissed); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if _, err := i.s.db.Exec(`UPDATE message_reports SET closed_at=CASE WHEN id=? THEN ? ELSE ? END WHERE id IN (?,?)`,
		expired, now.Add(-30*24*time.Hour).UnixMilli(), now.Add(-29*24*time.Hour).UnixMilli(), expired, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := i.s.db.Exec("UPDATE maintenance_audit SET occurred_at=? WHERE report_id=?", now.Add(-90*24*time.Hour).UnixMilli(), expired); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ops/data/export", "/ops/data/prune"} {
		call(t, owner, origin, "POST", path, nil, 404)
		call(t, peer, origin, "POST", path, nil, 404)
	}
	ctx := t.Context()
	dir := t.TempDir()
	out := filepath.Join(dir, "requester.json")
	if err := ExportAccountData(ctx, database, requester.ID, other.Username, out); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("mismatched requester export returned %v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched export left output, stat error %v", err)
	}
	if _, err := i.s.db.Exec(`CREATE TRIGGER fail_data_audit BEFORE INSERT ON maintenance_audit
		WHEN NEW.action IN ('data.export','data.prune') BEGIN SELECT RAISE(ABORT,'fixture blocked data audit'); END`); err != nil {
		t.Fatal(err)
	}
	if err := ExportAccountData(ctx, database, requester.ID, requester.Username, out); err == nil || !strings.Contains(err.Error(), "写入维护审计") {
		t.Fatalf("failed audit export returned %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("failed export left artifacts: %v, %v", entries, err)
	}
	if result, err := PruneGovernanceData(ctx, database); err == nil || result != (GovernanceCleanup{}) {
		t.Fatalf("failed audit prune returned %+v, %v", result, err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM message_reports", 4)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 2)
	if _, err := i.s.db.Exec("DROP TRIGGER fail_data_audit"); err != nil {
		t.Fatal(err)
	}
	if err := DisableLocalAccount(ctx, database, requester.ID); err != nil {
		t.Fatal(err)
	}
	if err := ExportAccountData(ctx, database, requester.ID, "  REQUESTER  ", out); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("export file permissions: %v, %v", info, err)
	}
	if err := ExportAccountData(ctx, database, requester.ID, requester.Username, out); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing export was not refused: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var exported accountExport
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Account.ID != requester.ID || exported.Account.Username != requester.Username || exported.Account.Status != "disabled" ||
		len(exported.Reports) != 2 || exported.Reports[0].ID != pending || exported.Reports[0].Status != "open" ||
		exported.Reports[1].ID != recent || exported.Reports[1].Status != "closed" || exported.Reports[1].ClosedAt == 0 {
		t.Fatalf("unexpected exported account: %+v", exported)
	}
	for _, denied := range []string{evidence, "password", "roomId", "messageId", "reporterUserId", "decision", `"username":"other"`} {
		if strings.Contains(string(data), denied) {
			t.Fatalf("export exposed %q", denied)
		}
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit WHERE action='data.export' AND target_user_id IS NOT NULL", 1)
	if result, err := PruneGovernanceData(ctx, database); err != nil || result != (GovernanceCleanup{ReportsDeleted: 1, AuditDeleted: 1}) {
		t.Fatalf("prune returned %+v, %v", result, err)
	}
	if result, err := PruneGovernanceData(ctx, database); err != nil || result != (GovernanceCleanup{}) {
		t.Fatalf("repeat prune returned %+v, %v", result, err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM message_reports", 3)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM messages", 4)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users", 2)
	entries, err := ListMaintenanceAudit(ctx, database, 0, 50)
	if err != nil || len(entries) != 5 || entries[1].Action != auditDataPrune || entries[1].Cleanup == nil ||
		*entries[1].Cleanup != (GovernanceCleanup{ReportsDeleted: 1, AuditDeleted: 1}) || entries[1].TargetUserID != 0 {
		t.Fatalf("unexpected audited cleanup: %+v %v", entries, err)
	}
}

func TestReplayReportClosureKeepsOriginalRetention(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin, database := i.host.URL, i.cfg.Database
	reporter, _ := addUser(t, origin, "reporter")
	author, target := addUser(t, origin, "author")
	room := createTestGroup(t, reporter, origin, "Snapshot report group", target.Username)
	message := sendReportFixtureMessage(t, origin, author, room, "Report before snapshot")
	id := submitReportFixture(t, origin, reporter, room, message, "spam", 201)
	closedAt := time.Now().Add(-31 * 24 * time.Hour).UnixMilli()
	if _, err := i.s.db.Exec("UPDATE message_reports SET created_at=? WHERE id=?", closedAt-1000, id); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, badTime := range []int64{0, closedAt - 1001, time.Now().Add(time.Hour).UnixMilli()} {
		if _, err := ReplayMessageReportClosure(ctx, database, id, ReportAccountDisabled, badTime); err == nil {
			t.Fatalf("replay accepted invalid time %d", badTime)
		}
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM message_reports WHERE closed_at IS NULL", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users WHERE status='active'", 2)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 0)
	for range 2 {
		result, err := ReplayMessageReportClosure(ctx, database, id, ReportAccountDisabled, closedAt)
		if err != nil || result.ClosedAt != closedAt || result.Decision != ReportAccountDisabled {
			t.Fatalf("replayed closure returned %+v, %v", result, err)
		}
	}
	if _, err := ReplayMessageReportClosure(ctx, database, id, ReportAccountDisabled, closedAt+1); err == nil {
		t.Fatal("replay rewrote the original closure time")
	}
	if _, err := ReviewMessageReport(ctx, database, id); err == nil {
		t.Fatal("normal review returned an expired case after replay")
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users WHERE status='disabled'", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 2)
	if result, err := PruneGovernanceData(ctx, database); err != nil || result != (GovernanceCleanup{ReportsDeleted: 1}) {
		t.Fatalf("prune after replay returned %+v, %v", result, err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM messages", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users WHERE status='disabled'", 1)
}
