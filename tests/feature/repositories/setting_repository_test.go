package repositories_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestListPlatformReadsOnlyPlatformRows(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)
	account := mocks.InsertAccount(t, "deposit-scan-settings")

	if err := repo.UpsertMany(ctx, account.ID, "deposit_scan", map[string]string{
		"batch_blocks": "999",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, ?, ?, ?, NOW(), NOW())`,
		"deposit_scan", "batch_blocks", "80",
	); err != nil {
		t.Fatalf("store platform row: %v", err)
	}

	rows, err := repo.ListPlatform(ctx, "deposit_scan")
	if err != nil {
		t.Fatalf("list platform: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "batch_blocks" || rows[0].Value != "80" || rows[0].AccountID != nil {
		t.Fatalf("platform rows = %+v", rows)
	}

	missing, err := repo.ListPlatform(ctx, "deposit_scan_absent")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing group = %+v, %v", missing, err)
	}
	if _, err := repo.ListPlatform(ctx, " "); err == nil {
		t.Fatal("expected a blank group to be rejected")
	}
}

func TestListGroupDoesNotReturnAnotherAccount(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)
	accountA := mocks.InsertAccount(t, "settings-scope-a")
	accountB := mocks.InsertAccount(t, "settings-scope-b")

	if err := repo.UpsertMany(ctx, accountA.ID, "account_security", map[string]string{
		"require_2fa": "true",
	}); err != nil {
		t.Fatalf("store account A: %v", err)
	}
	if err := repo.UpsertMany(ctx, accountB.ID, "account_security", map[string]string{
		"require_2fa": "false",
	}); err != nil {
		t.Fatalf("store account B: %v", err)
	}

	rowsA, err := repo.ListGroup(ctx, accountA.ID, "account_security")
	if err != nil || len(rowsA) != 1 || rowsA[0].Value != "true" || rowsA[0].AccountID == nil || *rowsA[0].AccountID != accountA.ID {
		t.Fatalf("account A = %+v, %v", rowsA, err)
	}
	rowsB, err := repo.ListGroup(ctx, accountB.ID, "account_security")
	if err != nil || len(rowsB) != 1 || rowsB[0].Value != "false" || rowsB[0].AccountID == nil || *rowsB[0].AccountID != accountB.ID {
		t.Fatalf("account B = %+v, %v", rowsB, err)
	}
}

func TestDeleteGroupLeavesPlatformRowsAndOtherAccounts(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)
	owner := mocks.InsertAccount(t, "reset-owner")
	other := mocks.InsertAccount(t, "reset-other")

	if err := repo.UpsertMany(ctx, owner.ID, "account_security", map[string]string{"require_2fa": "true"}); err != nil {
		t.Fatalf("store owner security: %v", err)
	}
	if err := repo.UpsertMany(ctx, owner.ID, "account_webhooks", map[string]string{"signing_secret": "enc:v1:kept"}); err != nil {
		t.Fatalf("store owner webhooks: %v", err)
	}
	if err := repo.UpsertMany(ctx, other.ID, "account_security", map[string]string{"require_2fa": "true"}); err != nil {
		t.Fatalf("store other security: %v", err)
	}
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'deposit_scan', 'batch_blocks', '80', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("store platform scan: %v", err)
	}

	if err := repo.DeleteGroup(ctx, owner.ID, "account_security"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := repo.ListGroup(ctx, owner.ID, "account_security")
	if err != nil || len(gone) != 0 {
		t.Fatalf("owner security = %+v, %v", gone, err)
	}
	kept, err := repo.ListGroup(ctx, owner.ID, "account_webhooks")
	if err != nil || len(kept) != 1 || kept[0].Value != "enc:v1:kept" {
		t.Fatalf("owner webhooks = %+v, %v", kept, err)
	}
	otherRows, err := repo.ListGroup(ctx, other.ID, "account_security")
	if err != nil || len(otherRows) != 1 {
		t.Fatalf("other security = %+v, %v", otherRows, err)
	}
	platform, err := repo.ListPlatform(ctx, "deposit_scan")
	if err != nil || len(platform) != 1 || platform[0].Value != "80" {
		t.Fatalf("platform scan = %+v, %v", platform, err)
	}
	if err := repo.DeleteGroup(ctx, owner.ID, " "); err == nil {
		t.Fatal("a blank group was deleted")
	}
	if err := repo.DeleteGroup(ctx, uuid.Nil, "account_security"); err == nil {
		t.Fatal("a nil account was deleted")
	}
}

type rollbackActivity struct {
	inner *repositories.AccountActivityRepository
}

func (w rollbackActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return w.inner.Within(ctx, fn)
}

func (w rollbackActivity) Append(ctx context.Context, row models.AccountActivity) error {
	if err := w.inner.Append(ctx, row); err != nil {
		return err
	}
	return errors.New("rollback")
}

func TestResetSectionCommitsWithActivityAndRollsBackTogether(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	settingsRepo := repositories.NewSettingRepository(nil)
	activityRepo := repositories.NewAccountActivityRepository(nil)
	account := mocks.InsertAccount(t, "reset-commit")
	actorID := uuid.New()
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		actorID, actorID.String()+"@example.com", "hash", "active",
	); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	if err := settingsRepo.UpsertMany(ctx, account.ID, "account_security", map[string]string{
		"require_2fa":          "true",
		"session_idle_minutes": "45",
	}); err != nil {
		t.Fatalf("store security: %v", err)
	}
	if err := settingsRepo.UpsertMany(ctx, account.ID, "account_sweep_limits", map[string]string{
		"max_addresses_evm": "9",
	}); err != nil {
		t.Fatalf("store sweep: %v", err)
	}
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, 'deposit_scan', 'concurrency', '4', NOW(), NOW())`,
	); err != nil {
		t.Fatalf("store scan: %v", err)
	}

	service := settings.NewService(settings.Deps{Store: settingsRepo, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityRepo})
	if _, err := service.ResetSection(ctx, account.ID, actorID, "owner", "security"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	security, err := settingsRepo.ListGroup(ctx, account.ID, "account_security")
	if err != nil || len(security) != 0 {
		t.Fatalf("security after reset = %+v, %v", security, err)
	}
	sweep, err := settingsRepo.ListGroup(ctx, account.ID, "account_sweep_limits")
	if err != nil || len(sweep) != 1 || sweep[0].Value != "9" {
		t.Fatalf("sweep after reset = %+v, %v", sweep, err)
	}
	scan, err := settingsRepo.ListPlatform(ctx, "deposit_scan")
	if err != nil || len(scan) != 1 || scan[0].Value != "4" {
		t.Fatalf("scan after reset = %+v, %v", scan, err)
	}
	enabled, err := service.Require2FA(ctx, account.ID)
	if err != nil || enabled {
		t.Fatalf("require_2fa after reset = %v, %v", enabled, err)
	}

	rows, total, err := activityRepo.List(ctx, account.ID, 20, 0)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("activity = %+v total %d err %v", rows, total, err)
	}
	if rows[0].Action != "settings.section_reset" || rows[0].TargetID != "security" {
		t.Fatalf("activity row = %+v", rows[0])
	}
	var meta string
	if err := facades.Orm().Query().Raw(
		`SELECT metadata::text FROM account_activity WHERE account_id = ? AND action = 'settings.section_reset'`,
		account.ID,
	).Scan(&meta); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var decoded struct {
		Group  string   `json:"group"`
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal([]byte(meta), &decoded); err != nil {
		t.Fatalf("metadata %s: %v", meta, err)
	}
	if decoded.Group != "account_security" || len(decoded.Fields) != 2 ||
		decoded.Fields[0] != "require_2fa" || decoded.Fields[1] != "session_idle_minutes" {
		t.Fatalf("metadata = %s", meta)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(meta), &raw); err != nil {
		t.Fatalf("metadata object: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("metadata stored an extra key: %s", meta)
	}

	if err := settingsRepo.UpsertMany(ctx, account.ID, "account_webhooks", map[string]string{
		"signing_secret": "enc:v1:still-here",
	}); err != nil {
		t.Fatalf("store secret: %v", err)
	}
	rolling := settings.NewService(settings.Deps{Store: settingsRepo, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: rollbackActivity{inner: activityRepo}})
	if _, err := rolling.ResetSection(ctx, account.ID, actorID, "owner", "webhooks"); err == nil {
		t.Fatal("reset committed after the activity write failed")
	}
	kept, err := settingsRepo.ListGroup(ctx, account.ID, "account_webhooks")
	if err != nil || len(kept) != 1 || kept[0].Value != "enc:v1:still-here" {
		t.Fatalf("webhooks after rollback = %+v, %v", kept, err)
	}
	_, total, err = activityRepo.List(ctx, account.ID, 20, 0)
	if err != nil || total != 1 {
		t.Fatalf("activity total after rollback = %d, %v", total, err)
	}
}

func TestUpsertPlatformReplacesTheSameKey(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)

	if err := repo.UpsertPlatform(ctx, "webhook_delivery", map[string]string{
		"max_attempts":    "4",
		"timeout_seconds": "7",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := repo.UpsertPlatform(ctx, "webhook_delivery", map[string]string{
		"max_attempts": "6",
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rows, err := repo.ListPlatform(ctx, "webhook_delivery")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, row := range rows {
		if row.AccountID != nil {
			t.Fatalf("platform row has an account: %+v", row)
		}
		got[row.Key] = row.Value
	}
	if got["max_attempts"] != "6" || got["timeout_seconds"] != "7" || len(got) != 2 {
		t.Fatalf("stored = %#v", got)
	}
}

func TestDeletePlatformLeavesAccountRows(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)
	account := mocks.InsertAccount(t, "platform-reset-delete")

	if err := repo.UpsertMany(ctx, account.ID, "mail_smtp", map[string]string{
		"host": "account-host.example.test",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}
	if err := repo.UpsertPlatform(ctx, "mail_smtp", map[string]string{
		"host": "smtp.example.test",
	}); err != nil {
		t.Fatalf("store platform mail: %v", err)
	}
	if err := repo.UpsertPlatform(ctx, "deposit_scan", map[string]string{
		"batch_blocks": "80",
	}); err != nil {
		t.Fatalf("store scan: %v", err)
	}

	if err := repo.DeletePlatform(ctx, " "); err == nil {
		t.Fatal("a blank group was deleted")
	}
	if err := repo.DeletePlatform(ctx, "mail_smtp"); err != nil {
		t.Fatalf("delete platform: %v", err)
	}

	accountRows, err := repo.ListGroup(ctx, account.ID, "mail_smtp")
	if err != nil || len(accountRows) != 1 || accountRows[0].Value != "account-host.example.test" {
		t.Fatalf("account mail = %+v, %v", accountRows, err)
	}
	platformMail, err := repo.ListPlatform(ctx, "mail_smtp")
	if err != nil || len(platformMail) != 0 {
		t.Fatalf("platform mail = %+v, %v", platformMail, err)
	}
	scan, err := repo.ListPlatform(ctx, "deposit_scan")
	if err != nil || len(scan) != 1 || scan[0].Value != "80" {
		t.Fatalf("scan = %+v, %v", scan, err)
	}
}

func TestResetPlatformSectionCommitsWithActivityAndRollsBackTogether(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	settingsRepo := repositories.NewSettingRepository(nil)
	activityRepo := repositories.NewAccountActivityRepository(nil)
	account := mocks.InsertAccount(t, "platform-reset-commit")
	actorID := uuid.New()
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		actorID, actorID.String()+"@example.com", "hash", "active",
	); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		actorID,
	); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	const secret = "platform-reset-db-secret"
	if err := settingsRepo.UpsertPlatform(ctx, "mail_smtp", map[string]string{
		"host":     "smtp.example.test",
		"password": "enc:v1:" + secret,
	}); err != nil {
		t.Fatalf("store mail: %v", err)
	}
	if err := settingsRepo.UpsertMany(ctx, account.ID, "account_security", map[string]string{
		"session_idle_minutes": "45",
	}); err != nil {
		t.Fatalf("store account security: %v", err)
	}
	if err := settingsRepo.UpsertPlatform(ctx, "deposit_scan", map[string]string{
		"batch_blocks": "80",
	}); err != nil {
		t.Fatalf("store scan: %v", err)
	}

	service := settings.NewService(settings.Deps{Store: settingsRepo, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityRepo}).
		WithPlatformAdmins(repositories.NewPlatformAdminRepository(nil))
	view, err := service.ResetPlatformSection(ctx, actorID, "mail")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if view.Name != "mail" {
		t.Fatalf("section = %q", view.Name)
	}
	mail, err := settingsRepo.ListPlatform(ctx, "mail_smtp")
	if err != nil || len(mail) != 0 {
		t.Fatalf("mail after reset = %+v, %v", mail, err)
	}
	kept, err := settingsRepo.ListGroup(ctx, account.ID, "account_security")
	if err != nil || len(kept) != 1 || kept[0].Value != "45" {
		t.Fatalf("account security = %+v, %v", kept, err)
	}
	scan, err := settingsRepo.ListPlatform(ctx, "deposit_scan")
	if err != nil || len(scan) != 1 || scan[0].Value != "80" {
		t.Fatalf("scan = %+v, %v", scan, err)
	}

	accountRows, accountTotal, err := activityRepo.List(ctx, account.ID, 20, 0)
	if err != nil || accountTotal != 0 || len(accountRows) != 0 {
		t.Fatalf("account activity = %+v total %d err %v", accountRows, accountTotal, err)
	}
	platformRows, platformTotal, err := activityRepo.ListPlatform(ctx, 20, 0)
	if err != nil {
		t.Fatalf("platform activity: %v", err)
	}
	mailGroups := 0
	for _, group := range settings.GroupsInSection("mail") {
		if group.Scope == settings.ScopePlatform {
			mailGroups++
		}
	}
	if platformTotal != int64(mailGroups) || len(platformRows) != mailGroups {
		t.Fatalf("platform activity = %d rows, total %d, want %d", len(platformRows), platformTotal, mailGroups)
	}
	sawSMTP := false
	for _, row := range platformRows {
		if row.AccountID != nil || row.Action != "settings.section_reset" || row.TargetID != "mail" {
			t.Fatalf("platform activity row = %+v", row)
		}
		encoded, err := json.Marshal(row.Metadata)
		if err != nil {
			t.Fatalf("metadata: %v", err)
		}
		meta := string(encoded)
		if strings.Contains(meta, secret) || strings.Contains(meta, "smtp.example.test") || strings.Contains(meta, "enc:v1:") {
			t.Fatalf("metadata held a value: %s", meta)
		}
		var decoded struct {
			Group  string   `json:"group"`
			Fields []string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(meta), &decoded); err != nil {
			t.Fatalf("metadata %s: %v", meta, err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(meta), &raw); err != nil {
			t.Fatalf("metadata object: %v", err)
		}
		if len(raw) != 2 || decoded.Group == "" || len(decoded.Fields) == 0 {
			t.Fatalf("metadata = %s", meta)
		}
		if decoded.Group == "mail_smtp" {
			sawSMTP = true
			if !stringListHas(decoded.Fields, "host", "password", "port") {
				t.Fatalf("mail fields = %v", decoded.Fields)
			}
		}
	}
	if !sawSMTP {
		t.Fatal("mail_smtp was not named in the activity")
	}
	var withAccount int64
	if err := facades.Orm().Query().Raw(
		`SELECT count(*) FROM account_activity WHERE account_id IS NOT NULL AND action = 'settings.section_reset'`,
	).Scan(&withAccount); err != nil {
		t.Fatalf("count account resets: %v", err)
	}
	if withAccount != 0 {
		t.Fatalf("account-scoped resets = %d", withAccount)
	}

	if err := settingsRepo.UpsertPlatform(ctx, "webhook_delivery", map[string]string{
		"max_attempts":    "4",
		"timeout_seconds": "9",
	}); err != nil {
		t.Fatalf("store delivery: %v", err)
	}
	rolling := settings.NewService(settings.Deps{Store: settingsRepo, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: rollbackActivity{inner: activityRepo}}).
		WithPlatformAdmins(repositories.NewPlatformAdminRepository(nil))
	if _, err := rolling.ResetPlatformSection(ctx, actorID, "delivery"); err == nil {
		t.Fatal("reset committed after the activity write failed")
	}
	delivery, err := settingsRepo.ListPlatform(ctx, "webhook_delivery")
	if err != nil || len(delivery) != 2 {
		t.Fatalf("delivery after rollback = %+v, %v", delivery, err)
	}
	_, platformTotal, err = activityRepo.ListPlatform(ctx, 20, 0)
	if err != nil || platformTotal != int64(mailGroups) {
		t.Fatalf("platform activity total after rollback = %d, %v", platformTotal, err)
	}
	_, accountTotal, err = activityRepo.List(ctx, account.ID, 20, 0)
	if err != nil || accountTotal != 0 {
		t.Fatalf("account activity total after rollback = %d, %v", accountTotal, err)
	}
}

func stringListHas(fields []string, want ...string) bool {
	got := map[string]bool{}
	for _, field := range fields {
		got[field] = true
	}
	for _, field := range want {
		if !got[field] {
			return false
		}
	}
	return true
}
