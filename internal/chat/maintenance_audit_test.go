package chat

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceAuditTracksAccountActionsAndSurvivesRestore(t *testing.T) {
	i := newInstance(t, Config{Registration: RegistrationOpen})
	origin := i.host.URL
	owner, account := addUser(t, origin, "owner")
	member, _ := addUser(t, origin, "member")
	createTestGroup(t, owner, origin, "Test group", "member")
	call(t, owner, origin, "GET", "/ops/audit", nil, 404)
	call(t, member, origin, "GET", "/ops/audit", nil, 404)
	if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_block_audit BEFORE INSERT ON maintenance_audit
		BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if code, err := IssueLocalAccountRecovery(t.Context(), i.cfg.Database, account.ID, false); err == nil || code != "" || !strings.Contains(err.Error(), "写入维护审计") {
		t.Fatalf("failed audit returned code or unexpected error: %v", err)
	}
	call(t, owner, origin, "GET", "/me", nil, 200)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM account_recovery_codes", 0)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 0)
	if _, err := i.s.db.Exec("DROP TRIGGER fixture_block_audit"); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UnixMilli()
	first := issueTestRecovery(t, i, account.ID, false)
	second := issueTestRecovery(t, i, account.ID, false)
	for range 2 {
		if err := DisableLocalAccount(t.Context(), i.cfg.Database, account.ID); err != nil {
			t.Fatal(err)
		}
	}
	if code, err := IssueLocalAccountRecovery(t.Context(), i.cfg.Database, account.ID, false); err == nil || code != "" {
		t.Fatal("ordinary issuance accepted a suspended account")
	}
	if _, err := i.s.db.Exec(`CREATE TRIGGER fixture_block_reopen_audit BEFORE INSERT ON maintenance_audit
		BEGIN SELECT RAISE(ABORT,'fixture blocked reopening audit'); END`); err != nil {
		t.Fatal(err)
	}
	if code, err := IssueLocalAccountRecovery(t.Context(), i.cfg.Database, account.ID, true); err == nil || code != "" || !strings.Contains(err.Error(), "写入维护审计") {
		t.Fatalf("failed reopening audit returned code or unexpected error: %v", err)
	}
	checkDBInt(t, i.s.db, "SELECT count(*) FROM users WHERE username='owner' AND status='disabled'", 1)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM account_recovery_codes", 0)
	checkDBInt(t, i.s.db, "SELECT count(*) FROM maintenance_audit", 4)
	if _, err := i.s.db.Exec("DROP TRIGGER fixture_block_reopen_audit"); err != nil {
		t.Fatal(err)
	}
	third := issueTestRecovery(t, i, account.ID, true)
	wantActions := []string{auditAccountReopen, auditAccountDisable, auditAccountDisable, auditIssueRecovery, auditIssueRecovery}
	wantResults := []string{auditSuccess, auditAlreadyDisabled, auditSuccess, auditSuccess, auditSuccess}
	pull := func(path string, before int64, limit int) []MaintenanceAuditEntry {
		t.Helper()
		rows, err := ListMaintenanceAudit(t.Context(), path, before, limit)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	firstPage := pull(i.cfg.Database, 0, 2)
	if len(firstPage) != 2 {
		t.Fatalf("first audit page has %d rows", len(firstPage))
	}
	all := append(firstPage, pull(i.cfg.Database, firstPage[1].ID, 50)...)
	if len(all) != len(wantActions) {
		t.Fatalf("got %d audit rows, want %d", len(all), len(wantActions))
	}
	for idx, row := range all {
		if row.Action != wantActions[idx] || row.Result != wantResults[idx] || row.TargetUserID != account.ID ||
			row.Source != "maintenance_cli" || row.OccurredAt < start || (idx > 0 && row.ID >= all[idx-1].ID) {
			t.Fatalf("unexpected audit row: %+v", row)
		}
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{first, second, third} {
		if strings.Contains(string(data), code) {
			t.Fatal("audit leaked a recovery code")
		}
	}

	olddate := time.Now().Add(-91 * 24 * time.Hour).UnixMilli()
	if _, err := i.s.db.Exec("UPDATE maintenance_audit SET occurred_at=? WHERE id=?", olddate, all[4].ID); err != nil {
		t.Fatal(err)
	}
	if rows := pull(i.cfg.Database, 0, 50); !slices.Equal(rows, all[:4]) {
		t.Fatalf("unexpected visible audit window: %v", rows)
	}
	backup := filepath.Join(t.TempDir(), "backup.sqlite")
	restored := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := CopyDatabase(t.Context(), i.cfg.Database, backup); err != nil {
		t.Fatal(err)
	}
	if err := CopyDatabase(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	if rows := pull(restored, 0, 50); !slices.Equal(rows, all[:4]) {
		t.Fatal("snapshot did not preserve the applicable audit rows and their timestamps")
	}
}
