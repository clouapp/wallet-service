package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

const dashboardGroupSecret = "dash-group-secret-value"

func TestAccountGroup_MemberSeesOneAccountAndHidesASecret(t *testing.T) {
	t.Parallel()

	accountA := uuid.New()
	accountB := uuid.New()
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	if err := store.UpsertMany(context.Background(), accountA, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "17",
	}); err != nil {
		t.Fatalf("store A: %v", err)
	}
	if err := store.UpsertMany(context.Background(), accountA, groupAccountWebhooks, map[string]string{
		keySigningSecret: "enc:v1:" + dashboardGroupSecret,
	}); err != nil {
		t.Fatalf("store secret: %v", err)
	}
	if err := store.UpsertMany(context.Background(), accountB, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "19",
		keyDailyWithdrawCapUSD: "8.75",
	}); err != nil {
		t.Fatalf("store B: %v", err)
	}
	store.extra = []models.Setting{{
		AccountID: &accountB,
		Group:     groupAccountSweepLimits,
		Key:       keyMaxAddressesBitcoin,
		Value:     "19",
	}}
	activity := &recordingActivity{}
	service := NewService(store, refuseOpenSealer{}, &memoryCache{}, activity)

	view, err := service.AccountGroup(context.Background(), accountA, "auditor", "  "+groupAccountSweepLimits+"  ")
	if err != nil {
		t.Fatalf("sweep limits: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), dashboardGroupSecret) || strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), "8.75") {
		t.Fatal("the group included a secret or the other account")
	}
	evm := fieldByKey(t, view, keyMaxAddressesEVM)
	if evm.Value != 17 || !evm.IsSet {
		t.Fatalf("evm = %+v", evm)
	}
	bitcoin := fieldByKey(t, view, keyMaxAddressesBitcoin)
	if bitcoin.IsSet || bitcoin.Value != defaultMaxAddressesBitcoin {
		t.Fatalf("bitcoin leaked the other account: %+v", bitcoin)
	}
	if view.ManagedBy != ManagedByPlatform || view.CanUpdate {
		t.Fatalf("platform-managed group = %+v", view)
	}

	webhooks, err := service.AccountGroup(context.Background(), accountA, "owner", groupAccountWebhooks)
	if err != nil {
		t.Fatalf("webhooks: %v", err)
	}
	encoded, err = json.Marshal(webhooks)
	if err != nil {
		t.Fatalf("webhooks view: %v", err)
	}
	if strings.Contains(string(encoded), dashboardGroupSecret) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the webhook group echoed a secret")
	}
	secret := fieldByKey(t, webhooks, keySigningSecret)
	if !secret.Secret || !secret.IsSet || secret.Value != nil {
		t.Fatalf("secret = %+v", secret)
	}
	if !webhooks.CanUpdate {
		t.Fatal("owner cannot update an account-managed group")
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
	for _, id := range store.listed {
		if id != accountA {
			t.Fatalf("listed %s", id)
		}
	}
}

func TestAccountGroup_OwnerAdminAndAuditorStillReadSweepLimits(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	if err := store.UpsertMany(context.Background(), accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "11",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	service := NewService(store, refuseOpenSealer{}, nopCache{}, &recordingActivity{})
	group, ok := FindGroup(groupAccountSweepLimits)
	if !ok || group.ViewPermission != "sweep.view" || group.UpdatePermission != "sweep.update" {
		t.Fatalf("account sweep permissions = %q %q present %v", group.ViewPermission, group.UpdatePermission, ok)
	}
	for _, role := range []string{"owner", "admin", "auditor"} {
		view, err := service.AccountGroup(context.Background(), accountID, role, groupAccountSweepLimits)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if view.Name != groupAccountSweepLimits || view.CanUpdate {
			t.Fatalf("%s group = %+v", role, view)
		}
		evm := fieldByKey(t, view, keyMaxAddressesEVM)
		if evm.Value != 11 || !evm.IsSet {
			t.Fatalf("%s evm = %+v", role, evm)
		}
	}
}

func TestAccountGroup_NotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	service := NewService(store, refuseOpenSealer{}, nopCache{}, &recordingActivity{})

	for _, name := range []string{"no-such-group", groupMailSMTP, groupSweepLimits, groupDepositScan} {
		_, err := service.AccountGroup(context.Background(), accountID, "user", name)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s = %v, want group not found", name, err)
		}
	}
	if len(store.listed) != 0 {
		t.Fatalf("unknown group listed %d", len(store.listed))
	}

	_, err := service.AccountGroup(context.Background(), accountID, "user", groupAccountSecurity)
	if !errors.Is(err, ErrViewForbidden) {
		t.Fatalf("user = %v", err)
	}
	if len(store.listed) != 0 {
		t.Fatalf("user listed %d", len(store.listed))
	}

	view, err := service.AccountGroup(context.Background(), accountID, "admin", groupAccountSecurity)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	if !view.CanUpdate || view.ManagedBy != ManagedByAccount {
		t.Fatalf("admin group = %+v", view)
	}
	auditor, err := service.AccountGroup(context.Background(), accountID, "auditor", groupAccountSecurity)
	if err != nil {
		t.Fatalf("auditor: %v", err)
	}
	if auditor.CanUpdate {
		t.Fatal("auditor can update")
	}
}
