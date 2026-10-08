package price

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/tests/memcache"
)

// recordingPriceCache is the in-memory driver plus a record of the calls the
// price service makes.
type recordingPriceCache struct {
	*memcache.Cache
	getKey string
	gets   int

	setKey   string
	setValue string
	setTTL   time.Duration
	setErr   error
	sets     int
}

func newRecordingPriceCache(stored string) *recordingPriceCache {
	cache := &recordingPriceCache{Cache: memcache.New()}
	if stored != "" {
		_ = cache.Cache.Put("currency:BTC", stored, 0)
	}
	return cache
}

func (r *recordingPriceCache) GetString(key string, def ...string) string {
	r.gets++
	r.getKey = key
	return r.Cache.GetString(key, def...)
}

func (r *recordingPriceCache) Put(key string, value any, ttl time.Duration) error {
	r.sets++
	r.setKey = key
	r.setValue = fmt.Sprint(value)
	r.setTTL = ttl
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

func TestGet_Price_USDSkipsTheCache(t *testing.T) {
	cache := newRecordingPriceCache("9")
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

func TestGet_Price_UsesTheCurrencyKey(t *testing.T) {
	cache := newRecordingPriceCache("42.5")
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

func TestGet_Price_NilCacheReadsTheStore(t *testing.T) {
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

func TestGet_Price_FallsThroughWhenTheCacheMisses(t *testing.T) {
	cases := []*recordingPriceCache{
		newRecordingPriceCache(""),
		newRecordingPriceCache("0"),
		newRecordingPriceCache("-1"),
		newRecordingPriceCache("not a number"),
	}
	for _, cache := range cases {
		store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("7")}}
		svc := NewService(Deps{Currencies: store, Cache: cache})

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

func TestCache_Price_NilCacheDoesNothing(t *testing.T) {
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}})
	svc.cachePrice(context.Background(), "BTC", mustPrice("42.5"))
}

func TestCache_Price_KeepsTheKeyTTLAndJSONNumber(t *testing.T) {
	cache := newRecordingPriceCache("")
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
	if cache.setValue != "42.5" {
		t.Fatalf("cached payload = %s", cache.setValue)
	}
}

func TestCache_Price_ContinuesWhenTheCacheFails(t *testing.T) {
	cache := newRecordingPriceCache("")
	cache.setErr = errors.New("boom")
	svc := NewService(Deps{Currencies: &cacheCurrencyStore{}, Cache: cache})

	svc.cachePrice(context.Background(), "ETH", mustPrice("1"))

	if cache.sets != 1 || cache.setKey != "currency:ETH" {
		t.Fatalf("sets = %d key = %q", cache.sets, cache.setKey)
	}
}

func TestProcess_Message_WritesTheCurrencyKey(t *testing.T) {
	cache := newRecordingPriceCache("")
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
	if cache.setValue != "42.5" {
		t.Fatalf("cached payload = %s", cache.setValue)
	}
}

func TestProcess_Message_SkipsANilCache(t *testing.T) {
	store := &cacheCurrencyStore{currency: &models.Currency{Code: "BTC", CurrentPrice: priceOf("1")}}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: store,
	})
	client.activeCodes = []string{"BTC"}

	client.processMessage(context.Background(), []byte(`{"asset_id_base":"BTC","rate":42.5}`))
}

func TestProcess_Message_IgnoresACacheError(t *testing.T) {
	cache := newRecordingPriceCache("")
	cache.setErr = errors.New("boom")
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
