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
	priceCoinGeckoFixture     = "price-coingecko-key-fixture"
	priceCoinMarketCapFixture = "price-coinmarketcap-key-fixture"
	priceCoinAPIFixture       = "price-coinapi-key-fixture"
)

type priceProviderCase struct {
	group    string
	fixture  string
	activity string
}

func priceProviderCases() []priceProviderCase {
	return []priceProviderCase{
		{
			group:    groupPriceCoinGecko,
			fixture:  priceCoinGeckoFixture,
			activity: `{"fields":["api_key","enabled"],"group":"price_coingecko"}`,
		},
		{
			group:    groupPriceCoinMarketCap,
			fixture:  priceCoinMarketCapFixture,
			activity: `{"fields":["api_key","enabled"],"group":"price_coinmarketcap"}`,
		},
		{
			group:    groupPriceCoinAPI,
			fixture:  priceCoinAPIFixture,
			activity: `{"fields":["api_key","enabled"],"group":"price_coinapi"}`,
		},
	}
}

func TestSavePlatformPriceSettings_StoresOrderAndSealsTheKey(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	for _, provider := range priceProviderCases() {
		saved, saveErr := service.SavePlatform(ctx, actor, provider.group, map[string]any{
			keyPriceEnabled: true,
			keyPriceAPIKey:  provider.fixture,
		})
		if saveErr != nil {
			t.Fatalf("save provider: %v", saveErr)
		}
		assertPriceViewHidesKey(t, saved, provider.fixture)
		stored := store.rows[platformStoreKey(provider.group)][keyPriceAPIKey]
		if stored == "" || !IsSealed(stored) {
			t.Fatal("the key was not sealed")
		}
		if store.rows[platformStoreKey(provider.group)][keyPriceEnabled] != "true" {
			t.Fatal("enabled was not stored")
		}
		row := activity.rows[len(activity.rows)-1]
		encoded, encodeErr := row.Metadata.Encode()
		if encodeErr != nil {
			t.Fatalf("metadata: %v", encodeErr)
		}
		if strings.Contains(encoded, provider.fixture) || strings.Contains(encoded, "enc:v1:") {
			t.Fatal("activity metadata included a price key")
		}
		if encoded != provider.activity || row.Action != "settings.updated" || row.TargetID != provider.group {
			t.Fatal("activity did not name the group and fields")
		}

		before := stored
		rowsBeforeBlank := len(activity.rows)
		if _, blankErr := service.SavePlatform(ctx, actor, provider.group, map[string]any{
			keyPriceAPIKey: "",
		}); blankErr != nil {
			t.Fatalf("blank key: %v", blankErr)
		}
		if store.rows[platformStoreKey(provider.group)][keyPriceAPIKey] != before {
			t.Fatal("a blank key wiped the stored key")
		}
		if len(activity.rows) != rowsBeforeBlank {
			t.Fatal("a blank key was recorded as a change")
		}
	}

	view, err := service.SavePlatform(ctx, actor, groupPriceLookup, map[string]any{
		keyProviderOrder: []any{priceProviderCoinGecko, priceProviderCoinMarketCap, priceProviderCoinAPI},
	})
	if err != nil {
		t.Fatalf("save order: %v", err)
	}
	if store.rows[platformStoreKey(groupPriceLookup)][keyProviderOrder] != "coingecko,coinmarketcap,coinapi" {
		t.Fatal("provider order was not stored")
	}
	assertPriceViewHidesKey(t, view, "")
	row := activity.rows[len(activity.rows)-1]
	if row.Action != "settings.updated" || row.TargetID != groupPriceLookup {
		t.Fatal("provider order was not recorded")
	}
	meta, err := row.Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta != `{"fields":["provider_order"],"group":"price_lookup"}` {
		t.Fatal("provider order activity named a value")
	}
}

func TestSavePlatformPriceLookup_RejectsAnUnknownProvider(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	_, err := service.SavePlatform(context.Background(), actor, groupPriceLookup, map[string]any{
		keyProviderOrder: []any{priceProviderCoinAPI, "kraken"},
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderOrder]) == 0 {
		t.Fatal("an unknown provider was accepted")
	}
	if _, stored := store.rows[platformStoreKey(groupPriceLookup)]; stored {
		t.Fatal("an unknown provider was stored")
	}

	_, err = service.SavePlatform(context.Background(), actor, groupPriceCoinAPI, map[string]any{
		keyPriceEnabled: "yes",
		keyPriceAPIKey:  priceCoinAPIFixture,
	})
	validation, ok = err.(*ValidationError)
	if !ok || len(validation.Fields[keyPriceEnabled]) == 0 {
		t.Fatal("a non-boolean enabled flag was accepted")
	}
	if _, stored := store.rows[platformStoreKey(groupPriceCoinAPI)]; stored {
		t.Fatal("an invalid provider write was stored")
	}
}

func TestSavePlatformPriceLookup_RejectsADisabledProvider(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()
	if _, err := service.SavePlatform(ctx, actor, groupPriceCoinGecko, map[string]any{
		keyPriceEnabled: true,
		keyPriceAPIKey:  priceCoinGeckoFixture,
	}); err != nil {
		t.Fatalf("enable coingecko: %v", err)
	}

	_, err := service.SavePlatform(ctx, actor, groupPriceLookup, map[string]any{
		keyProviderOrder: []any{priceProviderCoinGecko, priceProviderCoinAPI},
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderOrder]) == 0 || validation.Fields[keyProviderOrder][0] != priceOrderNotEnabled {
		t.Fatalf("disabled provider = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupPriceLookup)]; stored {
		t.Fatal("an order naming a disabled provider was stored")
	}
}

func TestSavePlatformPriceLookup_RefusesAnEmptyOrderInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	_, err := service.SavePlatform(context.Background(), actor, groupPriceLookup, map[string]any{
		keyProviderOrder: []any{},
	})
	validation, ok := err.(*ValidationError)
	if !ok || len(validation.Fields[keyProviderOrder]) == 0 || validation.Fields[keyProviderOrder][0] != priceOrderEmptyInProduction {
		t.Fatalf("empty production order = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupPriceLookup)]; stored {
		t.Fatal("an empty production order was stored")
	}
}

func TestSavePlatformPriceLookup_AllowsAnEmptyOrderOutsideProduction(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	store := newMemoryStore()
	actor := uuid.New()
	service := NewService(store, prefixSealer{}, &memoryCache{}, &recordingActivity{}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	if _, err := service.SavePlatform(context.Background(), actor, groupPriceLookup, map[string]any{
		keyProviderOrder: []any{},
	}); err != nil {
		t.Fatalf("empty order outside production: %v", err)
	}
	if store.rows[platformStoreKey(groupPriceLookup)][keyProviderOrder] != "" {
		t.Fatal("an empty order outside production was not stored")
	}
}

func TestSavePlatformPriceSettings_ForbidsANonAdmin(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{})
	_, err := service.SavePlatform(context.Background(), uuid.New(), groupPriceCoinAPI, map[string]any{
		keyPriceEnabled: true,
		keyPriceAPIKey:  priceCoinAPIFixture,
	})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	if _, stored := store.rows[platformStoreKey(groupPriceCoinAPI)]; stored {
		t.Fatal("a non-admin stored a price key")
	}
	if len(activity.rows) != 0 {
		t.Fatal("a non-admin was recorded")
	}
}

func TestSavePriceSettingsIsNotAnAccountGroup(t *testing.T) {
	t.Parallel()

	names := append([]string{groupPriceLookup}, priceProviderGroupNames()...)
	for _, name := range names {
		_, err := newTestService(newMemoryStore()).Save(
			context.Background(), uuid.New(), uuid.New(), "owner", name,
			map[string]any{keyPriceEnabled: true},
		)
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s error = %v, want group not found", name, err)
		}
	}
}

func assertPriceViewHidesKey(t *testing.T, view GroupView, fixture string) {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(string(encoded), "enc:v1:") || (fixture != "" && strings.Contains(string(encoded), fixture)) {
		t.Fatal("the response included a price key")
	}
}
