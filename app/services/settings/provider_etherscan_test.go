package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const providerEtherscanFixture = "provider-etherscan-key-fixture"

func TestSavePlatformEtherscan_StoresEnabledAndSealsTheAPIKey(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	saved, saveErr := service.SavePlatform(ctx, actor, groupProviderEtherscan, map[string]any{
		keyProviderEnabled: true,
		keyProviderAPIKey:  providerEtherscanFixture,
	})
	if saveErr != nil {
		t.Fatalf("save provider: %v", saveErr)
	}
	assertEtherscanViewHidesKey(t, saved)
	stored := store.rows[platformStoreKey(groupProviderEtherscan)][keyProviderAPIKey]
	if stored == "" || !IsSealed(stored) || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatal("the api key was not sealed")
	}
	if store.rows[platformStoreKey(groupProviderEtherscan)][keyProviderEnabled] != "true" {
		t.Fatal("enabled was not stored")
	}
	row := activity.rows[len(activity.rows)-1]
	encoded, encodeErr := row.Metadata.Encode()
	if encodeErr != nil {
		t.Fatalf("metadata: %v", encodeErr)
	}
	if strings.Contains(encoded, providerEtherscanFixture) || strings.Contains(encoded, "enc:v1:") {
		t.Fatal("activity metadata included the api key")
	}
	if encoded != `{"fields":["api_key","enabled"],"group":"provider_etherscan"}` || row.Action != "settings.updated" || row.TargetID != groupProviderEtherscan {
		t.Fatal("activity did not name the group and fields")
	}

	before := stored
	rowsBeforeBlank := len(activity.rows)
	if _, blankErr := service.SavePlatform(ctx, actor, groupProviderEtherscan, map[string]any{
		keyProviderAPIKey: "",
	}); blankErr != nil {
		t.Fatalf("blank key: %v", blankErr)
	}
	if store.rows[platformStoreKey(groupProviderEtherscan)][keyProviderAPIKey] != before {
		t.Fatal("a blank key wiped the stored key")
	}
	if len(activity.rows) != rowsBeforeBlank {
		t.Fatal("a blank key was recorded as a change")
	}
}

func TestSavePlatformEtherscan_RejectsAnAuthTokenAndANonBoolean(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	_, err := service.SavePlatform(ctx, actor, groupProviderEtherscan, map[string]any{
		keyProviderEnabled:   true,
		keyProviderAuthToken: providerEtherscanFixture,
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderAuthToken]) == 0 {
		t.Fatal("etherscan accepted an auth token")
	}
	if _, stored := store.rows[platformStoreKey(groupProviderEtherscan)]; stored {
		t.Fatal("an unknown etherscan key was stored")
	}

	_, err = service.SavePlatform(ctx, actor, groupProviderEtherscan, map[string]any{
		keyProviderEnabled: "yes",
		keyProviderAPIKey:  providerEtherscanFixture,
	})
	validation, ok = err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderEnabled]) == 0 {
		t.Fatal("a non-boolean enabled flag was accepted")
	}
	if _, stored := store.rows[platformStoreKey(groupProviderEtherscan)]; stored {
		t.Fatal("an invalid etherscan write was stored")
	}
}

func TestSavePlatformEtherscan_ForbidsANonAdmin(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{})

	_, err := service.SavePlatform(context.Background(), uuid.New(), groupProviderEtherscan, map[string]any{
		keyProviderEnabled: true,
		keyProviderAPIKey:  providerEtherscanFixture,
	})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupProviderEtherscan)]; stored {
		t.Fatal("a non-admin stored an api key")
	}
	if len(activity.rows) != 0 {
		t.Fatal("a non-admin was recorded")
	}
}

func TestSaveEtherscanIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	_, err := newTestService(newMemoryStore()).Save(
		context.Background(), uuid.New(), uuid.New(), "owner", groupProviderEtherscan,
		map[string]any{keyProviderEnabled: true},
	)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("error = %v, want group not found", err)
	}
}

func assertEtherscanViewHidesKey(t *testing.T, view GroupView) {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), providerEtherscanFixture) {
		t.Fatal("the response included the api key")
	}
}
