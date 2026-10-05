package price

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

const (
	quoteGeckoKey = "qg-opened-7c1e"
	quoteCMCKey   = "qcmc-opened-2a90"
	quoteEnvKey   = "qenv-coinapi-55b3"
	quoteNextKey  = "qg-opened-next-91d4"
)

func TestRefreshCryptoPrices_AsksTheEnabledProviderWithTheOpenedKey(t *testing.T) {
	logs := captureQuoteLogs(t)
	rows := &quoteSettingsRows{groups: map[string]map[string]string{
		"price_lookup": {"provider_order": "coingecko,coinmarketcap,kraken"},
		"price_coingecko": {
			"enabled": "true",
			"api_key": "enc:v1:" + quoteGeckoKey,
		},
		"price_coinmarketcap": {
			"enabled": "false",
			"api_key": "enc:v1:" + quoteCMCKey,
		},
	}}
	var asked []*askedQuoteProvider
	svc := newQuoteService(rows, quoteEnvKey, func(name, apiKey string) (PriceProvider, bool) {
		provider := &askedQuoteProvider{name: name, key: apiKey}
		asked = append(asked, provider)
		return provider, true
	})

	if err := svc.RefreshCryptoPrices(context.Background()); err != nil {
		t.Fatal("a settings quote failed the refresh")
	}
	if len(asked) != 1 || asked[0].name != providerCoinGecko || asked[0].calls != 1 || asked[0].key != quoteGeckoKey {
		t.Fatal("the enabled provider was not asked with the opened key")
	}
	if rows.groups["price_coinmarketcap"]["api_key"] == "" {
		t.Fatal("the disabled provider row disappeared")
	}

	rows.groups["price_coingecko"]["api_key"] = "enc:v1:" + quoteNextKey
	svc.currencies.cryptos[0].PriceUpdatedAt = nil
	if err := svc.RefreshCryptoPrices(context.Background()); err != nil {
		t.Fatal("the second quote failed")
	}
	if len(asked) != 2 || asked[1].key != quoteNextKey || asked[1].calls != 1 {
		t.Fatal("the second quote reused the key opened for the first")
	}
	requireQuoteLogsOmit(t, logs.String(), quoteGeckoKey, quoteCMCKey, quoteEnvKey, quoteNextKey, "enc:v1:")
}

func TestRefreshCryptoPrices_MissingSettingsUsesTheEnvCoinAPIKey(t *testing.T) {
	logs := captureQuoteLogs(t)
	var asked []*askedQuoteProvider
	svc := newQuoteService(&quoteSettingsRows{groups: map[string]map[string]string{}}, quoteEnvKey, func(name, apiKey string) (PriceProvider, bool) {
		provider := &askedQuoteProvider{name: name, key: apiKey}
		asked = append(asked, provider)
		return provider, true
	})

	if err := svc.RefreshCryptoPrices(context.Background()); err != nil {
		t.Fatal("a missing price_lookup row failed the refresh")
	}
	if len(asked) != 1 || asked[0].name != providerCoinAPI || asked[0].calls != 1 || asked[0].key != quoteEnvKey {
		t.Fatal("a missing settings row did not quote through the environment CoinAPI key")
	}
	requireQuoteLogsOmit(t, logs.String(), quoteEnvKey, "enc:v1:")
}

func TestRefreshCryptoPrices_FailedSettingsReadKeepsTheEnvKeyAndOmitsItFromLogs(t *testing.T) {
	logs := captureQuoteLogs(t)
	secret := quoteGeckoKey + " enc:v1:price-blob"
	var asked []*askedQuoteProvider
	svc := newQuoteService(&quoteSettingsRows{err: errors.New(secret)}, quoteEnvKey, func(name, apiKey string) (PriceProvider, bool) {
		provider := &askedQuoteProvider{name: name, key: apiKey}
		asked = append(asked, provider)
		return provider, true
	})

	if err := svc.RefreshCryptoPrices(context.Background()); err != nil {
		t.Fatal("a failed settings read failed the refresh")
	}
	if len(asked) != 1 || asked[0].name != providerCoinAPI || asked[0].key != quoteEnvKey || asked[0].calls != 1 {
		t.Fatal("a failed settings read did not keep the environment CoinAPI key")
	}
	requireQuoteLogsOmit(t, logs.String(), quoteGeckoKey, quoteEnvKey, "enc:v1:", "price-blob")
}

func TestRefreshFiatRates_ResolvesProvidersPerQuote(t *testing.T) {
	logs := captureQuoteLogs(t)
	rows := &quoteSettingsRows{groups: map[string]map[string]string{
		"price_lookup": {"provider_order": "coinmarketcap,coingecko"},
		"price_coingecko": {
			"enabled": "true",
			"api_key": "enc:v1:" + quoteGeckoKey,
		},
		"price_coinmarketcap": {
			"enabled": "false",
			"api_key": "enc:v1:" + quoteCMCKey,
		},
	}}
	reads := 0
	var asked []*askedQuoteProvider
	accountSettings := settings.NewService(rows, quotePrefixSealer{}, nil, quoteDiscardActivity{})
	currencies := &quoteCurrencyStore{fiats: []models.Currency{{Code: "EUR", Type: models.CurrencyTypeFiat}}}
	svc := NewService(Deps{Currencies: currencies}).
		WithSettingsSource(func(ctx context.Context) ([]Credential, error) {
			reads++
			return credentialsFrom(accountSettings.PriceProvidersForQuote(ctx))
		}).
		WithEnvCoinAPIKey(quoteEnvKey).
		withProviderFactory(func(name, apiKey string) (PriceProvider, bool) {
			provider := &askedQuoteProvider{name: name, key: apiKey}
			asked = append(asked, provider)
			return provider, true
		})

	if err := svc.RefreshFiatRates(context.Background()); err != nil {
		t.Fatal("a fiat quote failed")
	}
	if reads != 1 || len(asked) != 1 || asked[0].name != providerCoinGecko || asked[0].key != quoteGeckoKey || asked[0].fiatCalls != 1 {
		t.Fatal("the fiat quote did not ask the enabled provider with the opened key")
	}
	requireQuoteLogsOmit(t, logs.String(), quoteGeckoKey, quoteCMCKey, quoteEnvKey, "enc:v1:")
}

func TestRefreshCryptoPrices_WithoutASourceKeepsTheInjectedProviders(t *testing.T) {
	asked := &askedQuoteProvider{name: providerCoinAPI}
	svc := NewService(Deps{
		Providers: []PriceProvider{asked},
		Currencies: &quoteCurrencyStore{
			cryptos: []models.Currency{{Code: "BTC", Type: models.CurrencyTypeCrypto}},
		},
	}).WithEnvCoinAPIKey(quoteEnvKey)

	if err := svc.RefreshCryptoPrices(context.Background()); err != nil {
		t.Fatal("an injected provider quote failed")
	}
	if asked.calls != 1 {
		t.Fatal("a quote without settings did not keep the injected provider")
	}
}

type quoteService struct {
	*Service
	currencies *quoteCurrencyStore
}

func newQuoteService(rows *quoteSettingsRows, envKey string, factory quoteProviderFactory) *quoteService {
	accountSettings := settings.NewService(rows, quotePrefixSealer{}, nil, quoteDiscardActivity{})
	currencies := &quoteCurrencyStore{cryptos: []models.Currency{{Code: "BTC", Type: models.CurrencyTypeCrypto}}}
	svc := NewService(Deps{Currencies: currencies}).
		WithSettingsSource(func(ctx context.Context) ([]Credential, error) {
			return credentialsFrom(accountSettings.PriceProvidersForQuote(ctx))
		}).
		WithEnvCoinAPIKey(envKey).
		withProviderFactory(factory)
	return &quoteService{Service: svc, currencies: currencies}
}

func credentialsFrom(opened []settings.OpenedPriceProvider, err error) ([]Credential, error) {
	if err != nil {
		return nil, err
	}
	credentials := make([]Credential, 0, len(opened))
	for _, item := range opened {
		credentials = append(credentials, Credential{Name: item.Name, Key: item.Key})
	}
	return credentials, nil
}

type quoteCurrencyStore struct {
	cryptos []models.Currency
	fiats   []models.Currency
}

func (s *quoteCurrencyStore) FindByCode(context.Context, string) (*models.Currency, error) {
	return nil, nil
}

func (s *quoteCurrencyStore) FindActiveCryptos(context.Context) ([]models.Currency, error) {
	return s.cryptos, nil
}

func (s *quoteCurrencyStore) FindActiveFiats(context.Context) ([]models.Currency, error) {
	return s.fiats, nil
}

func (s *quoteCurrencyStore) FindStale(context.Context, string, time.Duration) ([]models.Currency, error) {
	return nil, nil
}

func (s *quoteCurrencyStore) SetPrice(context.Context, string, decimal.Decimal, decimal.Decimal) error {
	now := time.Now()
	for i := range s.cryptos {
		s.cryptos[i].PriceUpdatedAt = &now
	}
	for i := range s.fiats {
		s.fiats[i].PriceUpdatedAt = &now
	}
	return nil
}

type askedQuoteProvider struct {
	name      string
	key       string
	calls     int
	fiatCalls int
}

func (p *askedQuoteProvider) Name() string { return p.name }

func (p *askedQuoteProvider) FetchCryptoPrices([]string) (map[string]decimal.Decimal, error) {
	p.calls++
	return map[string]decimal.Decimal{"BTC": decimal.RequireFromString("10")}, nil
}

func (p *askedQuoteProvider) FetchFiatRates([]string) (map[string]decimal.Decimal, error) {
	p.fiatCalls++
	return map[string]decimal.Decimal{"EUR": decimal.RequireFromString("1.1")}, nil
}

type quoteSettingsRows struct {
	groups map[string]map[string]string
	err    error
}

func (s *quoteSettingsRows) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, s.err
}

func (s *quoteSettingsRows) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func (s *quoteSettingsRows) ListPlatform(_ context.Context, group string) ([]models.Setting, error) {
	if s.err != nil {
		return nil, s.err
	}
	values := s.groups[group]
	rows := make([]models.Setting, 0, len(values))
	for key, value := range values {
		rows = append(rows, models.Setting{Group: group, Key: key, Value: value})
	}
	return rows, nil
}

type quotePrefixSealer struct{}

func (quotePrefixSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + plaintext, nil
}

func (quotePrefixSealer) Open(value string) (string, error) {
	raw, ok := strings.CutPrefix(value, "enc:v1:")
	if !ok {
		return "", errors.New("open refused")
	}
	return raw, nil
}

type quoteDiscardActivity struct{}

func (quoteDiscardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (quoteDiscardActivity) Append(context.Context, models.AccountActivity) error { return nil }

func captureQuoteLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireQuoteLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a log line included a price api key")
		}
	}
}
