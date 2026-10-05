package price

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

type recordingPriceCache struct {
	value   string
	readErr error
	getKey  string
	gets    int

	setKey   string
	setValue []byte
	setTTL   time.Duration
	setErr   error
	sets     int
}

func (r *recordingPriceCache) Get(_ context.Context, key string) (string, error) {
	r.gets++
	r.getKey = key
	return r.value, r.readErr
}

func (r *recordingPriceCache) Set(_ context.Context, key string, value []byte, expiration time.Duration) error {
	r.sets++
	r.setKey = key
	r.setValue = append([]byte(nil), value...)
	r.setTTL = expiration
	return r.setErr
}

type cacheCurrencyStore struct {
	currency *models.Currency
	findErr  error
	setErr   error
	finds    int
}

func (s *cacheCurrencyStore) FindByCode(context.Context, string) (*models.Currency, error) {
	s.finds++
	return s.currency, s.findErr
}

func (s *cacheCurrencyStore) FindActiveCryptos(context.Context) ([]models.Currency, error) {
	return nil, nil
}

func (s *cacheCurrencyStore) FindActiveFiats(context.Context) ([]models.Currency, error) {
	return nil, nil
}

func (s *cacheCurrencyStore) FindStale(context.Context, string, time.Duration) ([]models.Currency, error) {
	return nil, nil
}

func (s *cacheCurrencyStore) SetPrice(context.Context, string, decimal.Decimal, decimal.Decimal) error {
	return s.setErr
}

func priceOf(text string) numeric.Decimal {
	value, err := decimal.NewFromString(text)
	if err != nil {
		panic(err)
	}
	return numeric.NewDecimal(value)
}

func mustPrice(text string) decimal.Decimal {
	value, err := decimal.NewFromString(text)
	if err != nil {
		panic(err)
	}
	return value
}

func TestGetPriceUSDSkipsTheCache(t *testing.T) {
	cache := &recordingPriceCache{value: "9"}
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}, Cache: cache})

	got, err := svc.GetPrice(context.Background(), "USD")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !got.Equal(mustPrice("1")) {
		t.Fatalf("price = %s", got)
	}
	if cache.gets != 0 {
		t.Fatalf("gets = %d", cache.gets)
	}
}

func TestGetPriceUsesTheCurrencyKey(t *testing.T) {
	cache := &recordingPriceCache{value: "42.5"}
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("1")}}
	svc := NewService(Deps{Currencies: store, Cache: cache})

	got, err := svc.GetPrice(context.Background(), "BTC")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !got.Equal(mustPrice("42.5")) {
		t.Fatalf("price = %s", got)
	}
	if cache.gets != 1 || cache.getKey != "currency:BTC" {
		t.Fatalf("gets = %d key = %q", cache.gets, cache.getKey)
	}
	if store.finds != 0 {
		t.Fatalf("store finds = %d", store.finds)
	}
}

func TestGetPriceNilCacheReadsTheStore(t *testing.T) {
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "ETH", CurrentPrice: priceOf("3200")}}
	svc := NewService(Deps{Currencies: store})

	got, err := svc.GetPrice(context.Background(), "ETH")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !got.Equal(mustPrice("3200")) {
		t.Fatalf("price = %s", got)
	}
	if store.finds != 1 {
		t.Fatalf("store finds = %d", store.finds)
	}
}

func TestGetPriceFallsThroughWhenTheCacheMisses(t *testing.T) {
	cases := []recordingPriceCache{
		{readErr: errors.New("miss")},
		{value: "0"},
		{value: "-1"},
	}
	for _, cache := range cases {
		store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("7")}}
		svc := NewService(Deps{Currencies: store, Cache: &cache})

		got, err := svc.GetPrice(context.Background(), "BTC")
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if !got.Equal(mustPrice("7")) || store.finds != 1 {
			t.Fatalf("price = %s finds = %d", got, store.finds)
		}
		if cache.getKey != "currency:BTC" {
			t.Fatalf("key = %q", cache.getKey)
		}
	}
}

func TestCachePriceNilCacheDoesNothing(t *testing.T) {
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}})
	svc.cachePrice(context.Background(), "BTC", mustPrice("42.5"))
}

func TestCachePriceKeepsTheKeyTTLAndJSONNumber(t *testing.T) {
	cache := &recordingPriceCache{}
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}, Cache: cache})

	svc.cachePrice(context.Background(), "BTC", mustPrice("42.5"))

	if cache.sets != 1 {
		t.Fatalf("sets = %d", cache.sets)
	}
	if cache.setKey != "currency:BTC" {
		t.Fatalf("key = %q", cache.setKey)
	}
	if cache.setTTL != redisCurrencyTTL {
		t.Fatalf("ttl = %s", cache.setTTL)
	}
	if !bytes.Equal(cache.setValue, []byte("42.5")) {
		t.Fatalf("cached payload = %s", cache.setValue)
	}
}

func TestCachePriceContinuesWhenRedisFails(t *testing.T) {
	cache := &recordingPriceCache{setErr: errors.New("boom")}
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}, Cache: cache})

	svc.cachePrice(context.Background(), "ETH", mustPrice("1"))

	if cache.sets != 1 || cache.setKey != "currency:ETH" {
		t.Fatalf("sets = %d key = %q", cache.sets, cache.setKey)
	}
}

func TestProcessMessageWritesTheCurrencyKey(t *testing.T) {
	cache := &recordingPriceCache{}
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("1")}}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: store,
		Cache:      cache,
	})
	client.activeCodes = []string{"BTC"}

	client.processMessage(context.Background(), []byte(`{"asset_id_base":"BTC","rate":42.5}`))

	if cache.sets != 1 {
		t.Fatalf("sets = %d", cache.sets)
	}
	if cache.setKey != "currency:BTC" {
		t.Fatalf("key = %q", cache.setKey)
	}
	if cache.setTTL != redisCurrencyTTL {
		t.Fatalf("ttl = %s", cache.setTTL)
	}
	if !bytes.Equal(cache.setValue, []byte("42.5")) {
		t.Fatalf("cached payload = %s", cache.setValue)
	}
}

func TestProcessMessageSkipsANilCache(t *testing.T) {
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("1")}}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: store,
	})
	client.activeCodes = []string{"BTC"}

	client.processMessage(context.Background(), []byte(`{"asset_id_base":"BTC","rate":42.5}`))
}

func TestProcessMessageIgnoresACacheError(t *testing.T) {
	cache := &recordingPriceCache{setErr: errors.New("boom")}
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("1")}}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: store,
		Cache:      cache,
	})
	client.activeCodes = []string{"BTC"}

	client.processMessage(context.Background(), []byte(`{"asset_id_base":"BTC","rate":42.5}`))

	if cache.sets != 1 || cache.setKey != "currency:BTC" {
		t.Fatalf("sets = %d key = %q", cache.sets, cache.setKey)
	}
}
