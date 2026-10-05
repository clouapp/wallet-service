package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestPriceProvidersForQuote_OpensEnabledProvidersInOrder(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupPriceLookup, map[string]string{
		keyProviderOrder: priceProviderCoinMarketCap + "," + priceProviderCoinGecko,
	})
	store.PutPlatform(groupPriceCoinMarketCap, map[string]string{
		keyPriceEnabled: "true",
		keyPriceAPIKey:  "enc:v1:" + priceCoinMarketCapFixture,
	})
	store.PutPlatform(groupPriceCoinGecko, map[string]string{
		keyPriceEnabled: "true",
		keyPriceAPIKey:  "enc:v1:" + priceCoinGeckoFixture,
	})
	store.PutPlatform(groupPriceCoinAPI, map[string]string{
		keyPriceEnabled: "true",
		keyPriceAPIKey:  "enc:v1:" + priceCoinAPIFixture,
	})

	got, err := newTestService(store).PriceProvidersForQuote(context.Background())
	if err != nil {
		t.Fatal("an ordered price read failed")
	}
	if len(got) != 2 || got[0].Name != priceProviderCoinMarketCap || got[1].Name != priceProviderCoinGecko {
		t.Fatal("providers were not returned in provider_order")
	}
	if got[0].Key != priceCoinMarketCapFixture || got[1].Key != priceCoinGeckoFixture {
		t.Fatal("an enabled provider was not opened")
	}
}

func TestPriceProvidersForQuote_SkipsDisabledUnsealedUnknownAndFailedReads(t *testing.T) {
	t.Parallel()

	t.Run("missing lookup", func(t *testing.T) {
		t.Parallel()
		got, err := newTestService(newMemoryStore()).PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 0 {
			t.Fatal("a missing price_lookup row did not yield an empty list")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		store := priceQuoteStore(map[string]map[string]string{
			groupPriceLookup: {keyProviderOrder: priceProviderCoinGecko},
			groupPriceCoinGecko: {
				keyPriceEnabled: "false",
				keyPriceAPIKey:  "enc:v1:" + priceCoinGeckoFixture,
			},
		})
		got, err := newTestService(store).PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 0 {
			t.Fatal("a disabled price provider was used")
		}
	})

	t.Run("unsealed", func(t *testing.T) {
		t.Parallel()
		store := priceQuoteStore(map[string]map[string]string{
			groupPriceLookup: {keyProviderOrder: priceProviderCoinAPI},
			groupPriceCoinAPI: {
				keyPriceEnabled: "true",
				keyPriceAPIKey:  priceCoinAPIFixture,
			},
		})
		got, err := newTestService(store).PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 0 {
			t.Fatal("an unsealed price api key was used")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		store := priceQuoteStore(map[string]map[string]string{
			groupPriceLookup: {keyProviderOrder: "kraken," + priceProviderCoinGecko},
			groupPriceCoinGecko: {
				keyPriceEnabled: "true",
				keyPriceAPIKey:  "enc:v1:" + priceCoinGeckoFixture,
			},
		})
		got, err := newTestService(store).PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 1 || got[0].Name != priceProviderCoinGecko || got[0].Key != priceCoinGeckoFixture {
			t.Fatal("an unknown provider was not skipped")
		}
	})

	t.Run("open failed", func(t *testing.T) {
		t.Parallel()
		store := priceQuoteStore(map[string]map[string]string{
			groupPriceLookup: {keyProviderOrder: priceProviderCoinGecko},
			groupPriceCoinGecko: {
				keyPriceEnabled: "true",
				keyPriceAPIKey:  "enc:v1:" + priceCoinGeckoFixture,
			},
		})
		service := NewService(Deps{Store: store, Sealer: refuseQuoteSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		got, err := service.PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 0 {
			t.Fatal("an api key whose seal did not open was used")
		}
	})

	t.Run("provider read failed", func(t *testing.T) {
		t.Parallel()
		inner := priceQuoteStore(map[string]map[string]string{
			groupPriceLookup: {keyProviderOrder: priceProviderCoinGecko + "," + priceProviderCoinAPI},
			groupPriceCoinGecko: {
				keyPriceEnabled: "true",
				keyPriceAPIKey:  "enc:v1:" + priceCoinGeckoFixture,
			},
			groupPriceCoinAPI: {
				keyPriceEnabled: "true",
				keyPriceAPIKey:  "enc:v1:" + priceCoinAPIFixture,
			},
		})
		store := selectivePriceStore{memoryStore: inner, failGroup: groupPriceCoinGecko, failErr: errors.New(priceCoinGeckoFixture)}
		got, err := newTestService(store).PriceProvidersForQuote(context.Background())
		if err != nil || len(got) != 1 || got[0].Name != priceProviderCoinAPI || got[0].Key != priceCoinAPIFixture {
			t.Fatal("a failed provider read dropped the rest of the order")
		}
	})

	t.Run("lookup read failed", func(t *testing.T) {
		t.Parallel()
		secret := priceCoinGeckoFixture + " enc:v1:price-blob"
		service := NewService(Deps{Store: platformErrStore{err: errors.New(secret)}, Sealer: prefixSealer{}, Cache: nopCache{}, Activity: discardActivity{}})
		_, err := service.PriceProvidersForQuote(context.Background())
		if err == nil || strings.Contains(err.Error(), priceCoinGeckoFixture) || strings.Contains(err.Error(), "enc:v1:") {
			t.Fatal("a failed price_lookup read exposed a key or was ignored")
		}
	})
}

func TestPriceProvidersForQuote_NilServiceOrContextFailsClosed(t *testing.T) {
	t.Parallel()

	var service *Service
	if _, err := service.PriceProvidersForQuote(context.Background()); err == nil {
		t.Fatal("a nil settings service returned providers")
	}
	if _, err := newTestService(newMemoryStore()).PriceProvidersForQuote(nil); err == nil {
		t.Fatal("a nil context returned providers")
	}
}

func priceQuoteStore(groups map[string]map[string]string) *memoryStore {
	store := newMemoryStore()
	for group, values := range groups {
		store.PutPlatform(group, values)
	}
	return store
}

type selectivePriceStore struct {
	*memoryStore
	failGroup string
	failErr   error
}

func (s selectivePriceStore) ListPlatform(ctx context.Context, group string) ([]models.Setting, error) {
	if group == s.failGroup {
		return nil, s.failErr
	}
	return s.memoryStore.ListPlatform(ctx, group)
}

type refuseQuoteSealer struct{}

func (refuseQuoteSealer) Seal(string) (string, error) {
	return "", errors.New("seal refused")
}

func (refuseQuoteSealer) Open(string) (string, error) {
	return "", errors.New("open refused")
}
