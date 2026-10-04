package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	providerAlchemyFixture   = "provider-alchemy-token-fixture"
	providerHeliusFixture    = "provider-helius-key-fixture"
	providerQuickNodeFixture = "provider-quicknode-key-fixture"
)

type webhookProviderCase struct {
	group     string
	secretKey string
	fixture   string
	activity  string
}

func webhookProviderCases() []webhookProviderCase {
	return []webhookProviderCase{
		{
			group:     groupProviderAlchemy,
			secretKey: keyProviderAuthToken,
			fixture:   providerAlchemyFixture,
			activity:  `{"fields":["auth_token","enabled"],"group":"provider_alchemy"}`,
		},
		{
			group:     groupProviderHelius,
			secretKey: keyProviderAPIKey,
			fixture:   providerHeliusFixture,
			activity:  `{"fields":["api_key","enabled"],"group":"provider_helius"}`,
		},
		{
			group:     groupProviderQuickNode,
			secretKey: keyProviderAPIKey,
			fixture:   providerQuickNodeFixture,
			activity:  `{"fields":["api_key","enabled"],"group":"provider_quicknode"}`,
		},
	}
}

func TestSavePlatformWebhookProviders_StoresEnabledAndSealsTheSecret(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	for _, provider := range webhookProviderCases() {
		body := map[string]any{keyProviderEnabled: true}
		body[provider.secretKey] = provider.fixture
		saved, saveErr := service.SavePlatform(ctx, actor, provider.group, body)
		if saveErr != nil {
			t.Fatalf("save provider: %v", saveErr)
		}
		assertWebhookProviderViewHidesSecret(t, saved, provider.fixture)
		stored := store.rows[platformStoreKey(provider.group)][provider.secretKey]
		if stored == "" || !IsSealed(stored) || !strings.HasPrefix(stored, "enc:v1:") {
			t.Fatal("the secret was not sealed")
		}
		if store.rows[platformStoreKey(provider.group)][keyProviderEnabled] != "true" {
			t.Fatal("enabled was not stored")
		}
		row := activity.rows[len(activity.rows)-1]
		encoded, encodeErr := row.Metadata.Encode()
		if encodeErr != nil {
			t.Fatalf("metadata: %v", encodeErr)
		}
		if strings.Contains(encoded, provider.fixture) || strings.Contains(encoded, "enc:v1:") {
			t.Fatal("activity metadata included a provider secret")
		}
		if encoded != provider.activity || row.Action != "settings.updated" || row.TargetID != provider.group {
			t.Fatal("activity did not name the group and fields")
		}

		before := stored
		rowsBeforeBlank := len(activity.rows)
		blank := map[string]any{}
		blank[provider.secretKey] = ""
		if _, blankErr := service.SavePlatform(ctx, actor, provider.group, blank); blankErr != nil {
			t.Fatalf("blank secret: %v", blankErr)
		}
		if store.rows[platformStoreKey(provider.group)][provider.secretKey] != before {
			t.Fatal("a blank secret wiped the stored secret")
		}
		if len(activity.rows) != rowsBeforeBlank {
			t.Fatal("a blank secret was recorded as a change")
		}
	}
}

func TestSavePlatformWebhookProviders_RejectsTheWrongSecretAndANonBoolean(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	_, err := service.SavePlatform(ctx, actor, groupProviderAlchemy, map[string]any{
		keyProviderEnabled: true,
		keyProviderAPIKey:  providerAlchemyFixture,
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderAPIKey]) == 0 {
		t.Fatal("alchemy accepted an api key")
	}
	if _, stored := store.rows[platformStoreKey(groupProviderAlchemy)]; stored {
		t.Fatal("an unknown alchemy key was stored")
	}

	_, err = service.SavePlatform(ctx, actor, groupProviderHelius, map[string]any{
		keyProviderEnabled:   true,
		keyProviderAuthToken: providerHeliusFixture,
	})
	validation, ok = err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderAuthToken]) == 0 {
		t.Fatal("helius accepted an auth token")
	}
	if _, stored := store.rows[platformStoreKey(groupProviderHelius)]; stored {
		t.Fatal("an unknown helius key was stored")
	}

	_, err = service.SavePlatform(ctx, actor, groupProviderQuickNode, map[string]any{
		keyProviderEnabled: "yes",
		keyProviderAPIKey:  providerQuickNodeFixture,
	})
	validation, ok = err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderEnabled]) == 0 {
		t.Fatal("a non-boolean enabled flag was accepted")
	}
	if _, stored := store.rows[platformStoreKey(groupProviderQuickNode)]; stored {
		t.Fatal("an invalid provider write was stored")
	}
}

func TestSavePlatformWebhookProviders_ForbidsANonAdmin(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{})

	for _, provider := range webhookProviderCases() {
		body := map[string]any{keyProviderEnabled: true}
		body[provider.secretKey] = provider.fixture
		_, err := service.SavePlatform(context.Background(), uuid.New(), provider.group, body)
		if !errors.Is(err, ErrPlatformForbidden) {
			t.Fatalf("non-admin = %v", err)
		}
		if _, stored := store.rows[platformStoreKey(provider.group)]; stored {
			t.Fatal("a non-admin stored a provider secret")
		}
	}
	if len(activity.rows) != 0 {
		t.Fatal("a non-admin was recorded")
	}
}

func TestSaveWebhookProvidersIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	for _, name := range webhookProviderGroupNames() {
		_, err := newTestService(newMemoryStore()).Save(
			context.Background(), uuid.New(), uuid.New(), "owner", name,
			map[string]any{keyProviderEnabled: true},
		)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s error = %v, want group not found", name, err)
		}
	}
}

func assertWebhookProviderViewHidesSecret(t *testing.T, view GroupView, fixture string) {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), "enc:v1:") || (fixture != "" && strings.Contains(string(encoded), fixture)) {
		t.Fatal("the response included a provider secret")
	}
}
