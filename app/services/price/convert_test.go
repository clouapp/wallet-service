package price

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
)

type mockCurrencyRepo struct {
	currencies map[string]*models.Currency
	stale      []models.Currency
	staleErr   error
	staleType  string
	staleFor   time.Duration
}

func (m *mockCurrencyRepo) FindActiveCryptos(context.Context) ([]models.Currency, error) {
	return nil, nil
}
func (m *mockCurrencyRepo) FindActiveFiats(context.Context) ([]models.Currency, error) {
	return nil, nil
}
func (m *mockCurrencyRepo) SetPrice(context.Context, string, float64, float64) error { return nil }

func (m *mockCurrencyRepo) FindStale(_ context.Context, currencyType string, staleDuration time.Duration) ([]models.Currency, error) {
	m.staleType = currencyType
	m.staleFor = staleDuration
	return m.stale, m.staleErr
}

func (m *mockCurrencyRepo) FindByCode(_ context.Context, code string) (*models.Currency, error) {
	if c, ok := m.currencies[code]; ok {
		return c, nil
	}
	return nil, nil
}

func newTestService() *Service {
	now := time.Now()
	repo := &mockCurrencyRepo{
		currencies: map[string]*models.Currency{
			"BTC": {ID: uuid.New(), Code: "BTC", CurrentPrice: 65000.0, PriceUpdatedAt: &now},
			"ETH": {ID: uuid.New(), Code: "ETH", CurrentPrice: 3200.0, PriceUpdatedAt: &now},
			"BRL": {ID: uuid.New(), Code: "BRL", CurrentPrice: 0.196, PriceUpdatedAt: &now},
			"EUR": {ID: uuid.New(), Code: "EUR", CurrentPrice: 1.09, PriceUpdatedAt: &now},
		},
	}
	return NewService(nil, repo, nil)
}

func TestConvertSameCurrency(t *testing.T) {
	svc := newTestService()
	result, err := svc.Convert(context.Background(), "BTC", "BTC", 1.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 1.5 {
		t.Errorf("expected 1.5, got %f", result)
	}
}

func TestConvertBTCtoUSD(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertToUSD(context.Background(), "BTC", 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 32500.0
	if result != expected {
		t.Errorf("expected %f, got %f", expected, result)
	}
}

func TestConvertBTCtoBRL(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertCryptoToFiat(context.Background(), "BTC", "BRL", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 65000.0 / 0.196
	if absDiff(result, expected) > 0.01 {
		t.Errorf("expected ~%f, got %f", expected, result)
	}
}

func TestConvertETHtoEUR(t *testing.T) {
	svc := newTestService()
	result, err := svc.ConvertCryptoToFiat(context.Background(), "ETH", "EUR", 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 2.0 * 3200.0 / 1.09
	if absDiff(result, expected) > 0.01 {
		t.Errorf("expected ~%f, got %f", expected, result)
	}
}

func TestConvertUnknownCurrency(t *testing.T) {
	svc := newTestService()
	_, err := svc.Convert(context.Background(), "UNKNOWN", "USD", 1.0)
	if err == nil {
		t.Error("expected error for unknown currency")
	}
}

func TestFindStaleReturnsTheStoreRows(t *testing.T) {
	want := []models.Currency{{Code: "BTC"}}
	repo := &mockCurrencyRepo{stale: want, staleErr: errStale}
	svc := NewService(nil, repo, nil)
	got, err := svc.FindStale(context.Background(), models.CurrencyTypeCrypto, time.Minute)
	if err != errStale {
		t.Fatalf("error = %v", err)
	}
	if len(got) != 1 || got[0].Code != "BTC" {
		t.Fatalf("rows = %+v", got)
	}
	if repo.staleType != models.CurrencyTypeCrypto || repo.staleFor != time.Minute {
		t.Fatalf("query = %s %s", repo.staleType, repo.staleFor)
	}
}

func TestPriceWebSocketUsesTheServiceCurrencyStore(t *testing.T) {
	repo := &mockCurrencyRepo{}
	svc := NewService(nil, repo, nil)
	client := svc.PriceWebSocket("key", nil)
	if client == nil || client.currencyRepo != repo || client.apiKey != "key" {
		t.Fatal("websocket client did not keep the service currency store")
	}
}

var errStale = errorString("stale")

type errorString string

func (e errorString) Error() string { return string(e) }

func absDiff(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
