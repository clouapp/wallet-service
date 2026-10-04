package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type allowPlatformAdmins struct {
	ids map[uuid.UUID]bool
}

func (a allowPlatformAdmins) Contains(_ context.Context, userID uuid.UUID) (bool, error) {
	return a.ids[userID], nil
}

func TestEffectiveWebhookDelivery_MissingRowKeepsZeros(t *testing.T) {
	t.Parallel()

	got, err := newTestService(newMemoryStore()).EffectiveWebhookDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective delivery: %v", err)
	}
	if got != (WebhookDeliveryValues{}) {
		t.Fatalf("effective = %+v, want zeros", got)
	}
}

func TestEffectiveWebhookDelivery_StoredRowOverridesBothKeys(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupWebhookDelivery, map[string]string{
		keyMaxAttempts:    "4",
		keyTimeoutSeconds: "7",
	})
	accountID := uuid.New()
	if err := store.UpsertMany(context.Background(), accountID, groupWebhookDelivery, map[string]string{
		keyMaxAttempts: "19",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}

	got, err := newTestService(store).EffectiveWebhookDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective delivery: %v", err)
	}
	if got != (WebhookDeliveryValues{MaxAttempts: 4, TimeoutSeconds: 7}) {
		t.Fatalf("effective = %+v, want the platform row", got)
	}
}

func TestEffectiveWebhookDelivery_InvalidKeyFallsBackToZero(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupWebhookDelivery, map[string]string{
		keyMaxAttempts:    "0",
		keyTimeoutSeconds: "nope",
	})
	got, err := newTestService(store).EffectiveWebhookDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective delivery: %v", err)
	}
	if got != (WebhookDeliveryValues{}) {
		t.Fatalf("effective = %+v, want zeros", got)
	}
}

func TestEffectiveWebhookDelivery_AttemptsAboveTheCeilingFallBack(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupWebhookDelivery, map[string]string{
		keyMaxAttempts:    "21",
		keyTimeoutSeconds: "3",
	})
	got, err := newTestService(store).EffectiveWebhookDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective delivery: %v", err)
	}
	if got != (WebhookDeliveryValues{TimeoutSeconds: 3}) {
		t.Fatalf("effective = %+v, want only the timeout", got)
	}
}

func TestEffectiveWebhookDelivery_StoreError(t *testing.T) {
	t.Parallel()

	_, err := newTestService(platformErrStore{err: errors.New("db down")}).EffectiveWebhookDelivery(context.Background())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v, want db down", err)
	}
}

func TestSavePlatform_ZeroOrNegativeIsNotStored(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	for _, body := range []map[string]any{
		{keyMaxAttempts: 0, keyTimeoutSeconds: 8},
		{keyMaxAttempts: -2, keyTimeoutSeconds: 8},
		{keyMaxAttempts: 21, keyTimeoutSeconds: 8},
		{keyMaxAttempts: 4, keyTimeoutSeconds: 0},
		{keyMaxAttempts: 4, keyTimeoutSeconds: -1},
	} {
		_, err := service.SavePlatform(ctx, actor, groupWebhookDelivery, body)
		validation, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("body %#v error = %v, want validation", body, err)
		}
		if len(validation.Fields) == 0 {
			t.Fatalf("body %#v stored a rejection with no fields", body)
		}
	}
	rows, err := store.ListPlatform(ctx, groupWebhookDelivery)
	if err != nil || len(rows) != 0 {
		t.Fatalf("stored rows = %+v, %v", rows, err)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want none", len(activity.rows))
	}
}

func TestSavePlatform_RecordsNamesAndDeliveryReadsThem(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	cache := &memoryCache{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, cache, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	if _, err := service.EffectiveWebhookDelivery(ctx); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	view, err := service.SavePlatform(ctx, actor, groupWebhookDelivery, map[string]any{
		keyMaxAttempts:    15,
		keyTimeoutSeconds: 9,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Name != groupWebhookDelivery || !view.CanUpdate {
		t.Fatalf("view = %+v", view)
	}
	got, err := service.EffectiveWebhookDelivery(ctx)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got != (WebhookDeliveryValues{MaxAttempts: 15, TimeoutSeconds: 9}) {
		t.Fatalf("effective = %+v", got)
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d", len(activity.rows))
	}
	row := activity.rows[0]
	if row.AccountID != nil || row.Action != "settings.updated" || row.TargetType != "settings" || row.TargetID != groupWebhookDelivery {
		t.Fatalf("activity = %+v", row)
	}
	encoded, err := row.Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if encoded != `{"fields":["max_attempts","timeout_seconds"],"group":"webhook_delivery"}` {
		t.Fatalf("metadata = %s", encoded)
	}
	if len(cache.keys) == 0 || cache.keys[len(cache.keys)-1] != "settings:platform:webhook_delivery" {
		t.Fatalf("forgotten keys = %#v", cache.keys)
	}
}

func TestSavePlatform_UnknownGroupIsNotFoundBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	stranger := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{})
	ctx := context.Background()

	_, err := service.SavePlatform(ctx, stranger, "no-such-group", map[string]any{keyMaxAttempts: 2})
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("unknown group = %v", err)
	}
	_, err = service.SavePlatform(ctx, stranger, groupAccountSecurity, map[string]any{keyRequire2FA: true})
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("account group = %v", err)
	}
	_, err = service.SavePlatform(ctx, stranger, groupWebhookDelivery, map[string]any{keyMaxAttempts: 2})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	rows, listErr := store.ListPlatform(ctx, groupWebhookDelivery)
	if listErr != nil || len(rows) != 0 {
		t.Fatalf("stored = %+v, %v", rows, listErr)
	}
}

func TestSaveWebhookDeliveryIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupWebhookDelivery,
		map[string]any{keyMaxAttempts: 4},
	)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("error = %v, want group not found", err)
	}
}

func TestValidateWebhookDelivery(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupWebhookDelivery)
	if !ok || group.Validate == nil {
		t.Fatal("webhook_delivery validator is missing")
	}
	if err := group.Validate(map[string]string{keyMaxAttempts: "1", keyTimeoutSeconds: "1"}); err != nil {
		t.Fatalf("lower bound: %v", err)
	}
	if err := group.Validate(map[string]string{keyMaxAttempts: "20", keyTimeoutSeconds: "10"}); err != nil {
		t.Fatalf("upper bound: %v", err)
	}
	err := group.Validate(map[string]string{keyMaxAttempts: "0", keyTimeoutSeconds: "-3"})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyMaxAttempts]) == 0 || len(validation.Fields[keyTimeoutSeconds]) == 0 {
		t.Fatalf("error = %#v", err)
	}
}
