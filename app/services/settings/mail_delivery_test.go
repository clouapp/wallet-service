package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEffectiveMailDelivery_MissingRowKeepsEveryFieldUnused(t *testing.T) {
	t.Parallel()

	got, err := newTestService(newMemoryStore()).EffectiveMailDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got != (MailDelivery{}) {
		t.Fatalf("effective = %+v, want the env from header", got)
	}
}

func TestEffectiveMailDelivery_UsesStoredHeaderAndSkipsABadField(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailDelivery, map[string]string{
		keyMailDriver:      mailDriverSMTP,
		keyMailFromAddress: "from-header@example.test",
		keyMailFromName:    "Macro",
	})
	got, err := newTestService(store).EffectiveMailDelivery(context.Background())
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if !got.UseAddress || got.Address != "from-header@example.test" || !got.UseName || got.Name != "Macro" {
		t.Fatalf("effective = %+v", got)
	}

	store.PutPlatform(groupMailDelivery, map[string]string{
		keyMailFromAddress: "not-an-email",
		keyMailFromName:    "",
	})
	invalid, err := newTestService(store).EffectiveMailDelivery(context.Background())
	if err != nil {
		t.Fatalf("invalid: %v", err)
	}
	if invalid.UseAddress || invalid.UseName || invalid.Address != "" || invalid.Name != "" {
		t.Fatal("an invalid mail_delivery field replaced the env from header")
	}
}

func TestEffectiveMailDelivery_StoreError(t *testing.T) {
	t.Parallel()

	_, err := newTestService(platformErrStore{err: errors.New("db down")}).EffectiveMailDelivery(context.Background())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v, want db down", err)
	}
}

func TestSavePlatformMailDelivery_StoresTheHeaderAndNamesTheFields(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	view, err := service.SavePlatform(ctx, actor, groupMailDelivery, map[string]any{
		keyMailDriver:      mailDriverSMTP,
		keyMailFromAddress: "from-header@example.test",
		keyMailFromName:    "Macro",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("the response sealed a from header")
	}
	if !strings.Contains(string(encoded), "from-header@example.test") || !strings.Contains(string(encoded), "Macro") {
		t.Fatal("the response hid a non-secret from header")
	}
	stored := store.rows[platformStoreKey(groupMailDelivery)]
	if stored[keyMailFromAddress] != "from-header@example.test" || stored[keyMailFromName] != "Macro" || stored[keyMailDriver] != mailDriverSMTP {
		t.Fatalf("stored = %#v", stored)
	}
	if strings.HasPrefix(stored[keyMailFromAddress], "enc:v1:") || strings.HasPrefix(stored[keyMailFromName], "enc:v1:") {
		t.Fatal("the from header was sealed")
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d", len(activity.rows))
	}
	meta, err := activity.rows[0].Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	const want = `{"fields":["driver","from_address","from_name"],"group":"mail_delivery"}`
	if meta != want || strings.Contains(meta, "from-header@example.test") || strings.Contains(meta, "Macro") {
		t.Fatalf("activity metadata = %s", meta)
	}

	_, err = service.SavePlatform(ctx, actor, groupMailDelivery, map[string]any{keyMailFromAddress: "not-an-email"})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyMailFromAddress]) == 0 {
		t.Fatalf("invalid address = %v", err)
	}
	if store.rows[platformStoreKey(groupMailDelivery)][keyMailFromAddress] != "from-header@example.test" {
		t.Fatal("an invalid address was stored")
	}

	_, err = service.SavePlatform(ctx, actor, groupMailDelivery, map[string]any{keyMailFromName: ""})
	validation, ok = err.(*ValidationError)
	if !ok || len(validation.Fields[keyMailFromName]) == 0 {
		t.Fatalf("empty name = %v", err)
	}
	if store.rows[platformStoreKey(groupMailDelivery)][keyMailFromName] != "Macro" {
		t.Fatal("an empty name wiped the stored name")
	}
	if len(activity.rows) != 1 {
		t.Fatal("a rejected write was recorded")
	}

	opened, err := service.EffectiveMailDelivery(ctx)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if !opened.UseAddress || opened.Address != "from-header@example.test" || !opened.UseName || opened.Name != "Macro" {
		t.Fatalf("mailer header = %+v", opened)
	}

	stranger := uuid.New()
	_, err = service.SavePlatform(ctx, stranger, groupMailDelivery, map[string]any{
		keyMailFromAddress: "other@example.test",
		keyMailFromName:    "Other",
	})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	if store.rows[platformStoreKey(groupMailDelivery)][keyMailFromAddress] != "from-header@example.test" {
		t.Fatal("a non-admin write was stored")
	}
}

func TestSavePlatformMailDelivery_RefusesLogInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	_, err := service.SavePlatform(context.Background(), actor, groupMailDelivery, map[string]any{
		keyMailDriver:      mailDriverLog,
		keyMailFromAddress: "from-header@example.test",
		keyMailFromName:    "Macro",
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyMailDriver]) == 0 {
		t.Fatalf("log in production = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupMailDelivery)]; stored {
		t.Fatal("log in production was stored")
	}
}

func TestSaveMailDeliveryIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupMailDelivery,
		map[string]any{keyMailFromAddress: "from-header@example.test"},
	)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("error = %v, want group not found", err)
	}
}
