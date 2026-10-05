package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

const accountGroupSecret = "acct-group-secret-value"

type setAccounts struct {
	ids   map[uuid.UUID]bool
	calls int
}

func (a *setAccounts) Exists(_ context.Context, id uuid.UUID) (bool, error) {
	a.calls++
	return a.ids[id], nil
}

type recordingAccountStore struct {
	*memoryStore
	listed []uuid.UUID
	extra  []models.Setting
}

func (s *recordingAccountStore) ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error) {
	s.listed = append(s.listed, accountID)
	rows, err := s.memoryStore.ListGroup(ctx, accountID, group)
	if err != nil {
		return nil, err
	}
	return append(rows, s.extra...), nil
}

type refuseOpenSealer struct{}

func (refuseOpenSealer) Seal(string) (string, error) {
	return "", errors.New("seal")
}

func (refuseOpenSealer) Open(string) (string, error) {
	return "", errors.New("opened a secret")
}

func TestPlatformAccountGroup_AdminSeesOneAccountAndHidesASecret(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountA := uuid.New()
	accountB := uuid.New()
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	if err := store.UpsertMany(context.Background(), accountA, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "17",
	}); err != nil {
		t.Fatalf("store A: %v", err)
	}
	if err := store.UpsertMany(context.Background(), accountB, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "19",
		keyDailyWithdrawCapUSD: "8.75",
	}); err != nil {
		t.Fatalf("store B: %v", err)
	}
	store.PutPlatform(groupSweepLimits, map[string]string{keyMaxAddressesSolana: "3"})
	secretRow := models.Setting{
		AccountID: &accountA,
		Group:     groupAccountSweepLimits,
		Key:       keySigningSecret,
		Value:     "enc:v1:" + accountGroupSecret,
	}
	other := models.Setting{
		AccountID: &accountB,
		Group:     groupAccountSweepLimits,
		Key:       keyMaxAddressesBitcoin,
		Value:     "19",
	}
	store.extra = []models.Setting{secretRow, other}
	activity := &recordingActivity{}
	cache := &memoryCache{}
	emptyAccount := uuid.New()
	service := NewService(Deps{Store: store, Sealer: refuseOpenSealer{}, Cache: cache, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithAccounts(&setAccounts{ids: map[uuid.UUID]bool{
			accountA: true, accountB: true, emptyAccount: true,
		}})
	empty, err := service.PlatformAccountGroup(context.Background(), actor, emptyAccount, groupAccountSweepLimits)
	if err != nil {
		t.Fatalf("unstored group: %v", err)
	}
	evm := fieldByKey(t, empty, keyMaxAddressesEVM)
	if evm.IsSet || evm.Value != defaultMaxAddressesEVM || evm.Secret {
		t.Fatalf("unstored evm = %+v", evm)
	}
	solana := fieldByKey(t, empty, keyMaxAddressesSolana)
	if solana.IsSet || solana.Value != defaultMaxAddressesSolana {
		t.Fatalf("unstored solana used a platform row: %+v", solana)
	}

	view, err := service.PlatformAccountGroup(context.Background(), actor, accountA, "  "+groupAccountSweepLimits+"  ")
	if err != nil {
		t.Fatalf("stored group: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), accountGroupSecret) || strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), "8.75") {
		t.Fatal("the group included a secret or the other account")
	}
	evm = fieldByKey(t, view, keyMaxAddressesEVM)
	if evm.Value != 17 || !evm.IsSet || evm.Secret {
		t.Fatalf("evm = %+v", evm)
	}
	bitcoin := fieldByKey(t, view, keyMaxAddressesBitcoin)
	if bitcoin.IsSet || bitcoin.Value != defaultMaxAddressesBitcoin {
		t.Fatalf("bitcoin leaked the other account: %+v", bitcoin)
	}
	if view.Scope != ScopeAccount || view.ManagedBy != ManagedByPlatform || !view.CanUpdate {
		t.Fatalf("group = %+v", view)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
	if len(cache.keys) != 0 {
		t.Fatalf("cache keys = %v", cache.keys)
	}
	for _, id := range store.listed {
		if id != emptyAccount && id != accountA {
			t.Fatalf("listed %s", id)
		}
	}
}

func TestPlatformAccountGroup_NotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	admins := &countingPlatformAdmins{}
	accounts := &setAccounts{ids: map[uuid.UUID]bool{accountID: true}}
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	service := NewService(Deps{Store: store, Sealer: refuseOpenSealer{}, Cache: nopCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(admins).
		WithAccounts(accounts)

	for _, name := range []string{"no-such-group", groupMailSMTP, groupAccountSecurity, groupAccountWebhooks} {
		_, err := service.PlatformAccountGroup(context.Background(), actor, accountID, name)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s = %v, want group not found", name, err)
		}
	}
	if accounts.calls != 0 || admins.calls != 0 || len(store.listed) != 0 {
		t.Fatalf("ineligible group looked up account %d admin %d rows %d", accounts.calls, admins.calls, len(store.listed))
	}

	_, err := service.PlatformAccountGroup(context.Background(), actor, uuid.Nil, groupAccountSweepLimits)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("nil account = %v", err)
	}
	_, err = service.PlatformAccountGroup(context.Background(), actor, uuid.New(), groupAccountSweepLimits)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("unknown account = %v", err)
	}
	if admins.calls != 0 || len(store.listed) != 0 {
		t.Fatalf("unknown account checked admin %d or listed %d", admins.calls, len(store.listed))
	}

	_, err = service.PlatformAccountGroup(context.Background(), actor, accountID, groupAccountSweepLimits)
	if !errors.Is(err, ErrPlatformViewForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	if len(store.listed) != 0 {
		t.Fatalf("non-admin listed %d rows", len(store.listed))
	}
	if len(service.activity.(*recordingActivity).rows) != 0 {
		t.Fatal("a forbidden read wrote activity")
	}
}

func TestRenderStoredGroupOmitsASecret(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	group := Group{
		Name: groupAccountWebhooks,
		Settings: []Definition{{
			Key:    keySigningSecret,
			Type:   TypeString,
			Secret: true,
		}},
	}
	updated := time.Now().UTC()
	view := renderStoredGroup(group, []models.Setting{{
		AccountID: &accountID,
		Group:     group.Name,
		Key:       keySigningSecret,
		Value:     "enc:v1:" + accountGroupSecret,
		UpdatedAt: updated,
	}}, true)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), accountGroupSecret) || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the group included a secret")
	}
	secret := fieldByKey(t, view, keySigningSecret)
	if !secret.Secret || !secret.IsSet || secret.Value != nil {
		t.Fatal("the secret field was not write-only")
	}
}

func TestSettingsService_GetGroup_CarriesUpdatedAtOnlyForStoredValues(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupAccountSecurity)
	if !ok {
		t.Fatal("account_security is not in the registry")
	}
	if updated := renderStoredGroup(group, nil, false).UpdatedAt; updated != nil {
		t.Fatal("a group on its defaults carried a timestamp")
	}
	unset := renderStoredGroup(group, []models.Setting{{
		Key:   keySessionIdleMinutes,
		Value: "45",
	}}, false)
	if unset.UpdatedAt != nil {
		t.Fatal("a stored row with no timestamp was dated")
	}
	idle := fieldByKey(t, unset, keySessionIdleMinutes)
	if !idle.IsSet || idle.Value != 45 {
		t.Fatalf("stored idle = %+v", idle)
	}
	require2FA := fieldByKey(t, unset, keyRequire2FA)
	if require2FA.IsSet || require2FA.Value != false {
		t.Fatalf("default require_2fa = %+v", require2FA)
	}

	written := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	stored := renderStoredGroup(group, []models.Setting{{
		Key:       keySessionIdleMinutes,
		Value:     "45",
		UpdatedAt: written,
	}}, false)
	if stored.UpdatedAt == nil || !stored.UpdatedAt.Equal(written) {
		t.Fatal("the stored row's timestamp was not returned")
	}
}

func TestSettingsService_GetSettings_ServesOnlyTheKeysTheCatalogStillDeclares(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupAccountWebhooks)
	if !ok {
		t.Fatal("account_webhooks is not in the registry")
	}
	const retired = "catalog-retired-value"
	const sealed = "enc:v1:catalog-sealed-value"
	view := renderStoredGroup(group, []models.Setting{
		{Key: keySigningSecret, Value: sealed},
		{Key: keyDefaultEvents, Value: "deposit.confirmed"},
		{Key: "legacy_token", Value: retired},
	}, true)
	if len(view.Fields) != len(group.Settings) {
		t.Fatalf("fields = %d, catalog = %d", len(view.Fields), len(group.Settings))
	}
	for _, field := range view.Fields {
		if field.Key == "legacy_token" {
			t.Fatal("a retired key was still served")
		}
	}
	secret := fieldByKey(t, view, keySigningSecret)
	if !secret.Secret || !secret.IsSet || secret.Value != nil {
		t.Fatal("the signing secret was not reduced to is_set")
	}
	events := fieldByKey(t, view, keyDefaultEvents)
	items, ok := events.Value.([]string)
	if !ok || len(items) != 1 || items[0] != "deposit.confirmed" {
		t.Fatalf("default events = %#v", events.Value)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), retired) || strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), "catalog-sealed-value") {
		t.Fatal("the document included a secret or a retired value")
	}
}
