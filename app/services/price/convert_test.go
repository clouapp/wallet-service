package price

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

type mockCurrencyRepo struct {
	currencies map[string]*models.Currency
	active     []models.Currency
	stale      []models.Currency
	staleErr   error
	staleType  string
	staleFor   time.Duration
}

func (m *mockCurrencyRepo) FindActiveCryptos(context.Context) ([]models.Currency, error) {
	return m.active, nil
}
func (m *mockCurrencyRepo) FindActiveFiats(context.Context) ([]models.Currency, error) {
	return nil, nil
}
func (m *mockCurrencyRepo) SetPrice(context.Context, string, decimal.Decimal, decimal.Decimal) error {
	return nil
}

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

func mustDecimal(t *testing.T, text string) decimal.Decimal {
	t.Helper()
	value, err := decimal.NewFromString(text)
	if err != nil {
		t.Fatalf("decimal %q: %v", text, err)
	}
	return value
}

func priced(t *testing.T, code, price string) *models.Currency {
	t.Helper()
	now := time.Now()
	return &models.Currency{ID: uuid.New(), Code: code, CurrentPrice: numeric.NewDecimal(mustDecimal(t, price)), PriceUpdatedAt: &now}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	repo := &mockCurrencyRepo{
		currencies: map[string]*models.Currency{
			"BTC":  priced(t, "BTC", "65000"),
			"ETH":  priced(t, "ETH", "3200"),
			"BRL":  priced(t, "BRL", "0.196"),
			"EUR":  priced(t, "EUR", "1.09"),
			"DIME": priced(t, "DIME", "0.1"),
			"FREE": priced(t, "FREE", "0"),
		},
	}
	return NewService(nil, repo, nil)
}

func requireDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(mustDecimal(t, want)) {
		t.Fatalf("got %s, want %s", got.String(), want)
	}
}

func TestConvertSameCurrency(t *testing.T) {
	result, err := newTestService(t).Convert(context.Background(), "BTC", "BTC", mustDecimal(t, "1.5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireDecimal(t, result, "1.5")
}

func TestConvertBTCtoUSD(t *testing.T) {
	result, err := newTestService(t).ConvertToUSD(context.Background(), "BTC", mustDecimal(t, "0.5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireDecimal(t, result, "32500")
}

func TestConvertBTCtoBRLRoundsToTheConversionScale(t *testing.T) {
	result, err := newTestService(t).ConvertCryptoToFiat(context.Background(), "BTC", "BRL", mustDecimal(t, "1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireDecimal(t, result, "331632.653061224489795918")
}

func TestConvertETHtoEURRoundsHalfAwayFromZero(t *testing.T) {
	result, err := newTestService(t).ConvertCryptoToFiat(context.Background(), "ETH", "EUR", mustDecimal(t, "2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireDecimal(t, result, "5871.559633027522935780")
}

func TestConvertIsExactWhereFloatsDrift(t *testing.T) {
	result, err := newTestService(t).ConvertToUSD(context.Background(), "DIME", mustDecimal(t, "3"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireDecimal(t, result, "0.3")
}

func TestConvertUnknownCurrency(t *testing.T) {
	_, err := newTestService(t).Convert(context.Background(), "UNKNOWN", "USD", mustDecimal(t, "1"))
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
	dialer := stubDialer{}
	svc := NewService(nil, repo, nil).WithQuoteDialer(dialer)
	client := svc.PriceWebSocket("key", nil)
	if client == nil || client.currencyRepo != repo || client.apiKey != "key" || client.dialer != dialer {
		t.Fatal("websocket client did not keep the service currency store")
	}
}

type stubDialer struct{}

func (stubDialer) Dial(context.Context, string) (QuoteConn, error) {
	return nil, errStale
}

var errStale = errorString("stale")

type errorString string

func (e errorString) Error() string { return string(e) }

func TestConvertRejectsAZeroTargetPrice(t *testing.T) {
	_, err := newTestService(t).Convert(context.Background(), "BTC", "FREE", mustDecimal(t, "1"))
	if err == nil {
		t.Fatal("expected an error for a zero target price")
	}
}

func TestConvertRejectsANegativeAmount(t *testing.T) {
	_, err := newTestService(t).Convert(context.Background(), "BTC", "USD", mustDecimal(t, "-1"))
	if !errors.Is(err, numeric.ErrNegative) {
		t.Fatalf("err = %v, want ErrNegative", err)
	}
}

func TestConvertRequiresBothCodes(t *testing.T) {
	if _, err := newTestService(t).Convert(context.Background(), " ", "USD", mustDecimal(t, "1")); err == nil {
		t.Fatal("expected an error for an empty source code")
	}
}

func TestInvertRateKeepsThePriceColumnScale(t *testing.T) {
	requireDecimal(t, invertRate(mustDecimal(t, "5.1")), "0.1960784314")
	requireDecimal(t, invertRate(mustDecimal(t, "0")), "0")
}

func TestFitQuotedPriceSkipsQuotesThatRoundToZeroOrOverflow(t *testing.T) {
	if _, ok := fitQuotedPrice("TINY", mustDecimal(t, "0.00000000001")); ok {
		t.Fatal("a quote that rounds to zero must be skipped")
	}
	if _, ok := fitQuotedPrice("HUGE", mustDecimal(t, "1e18")); ok {
		t.Fatal("a quote above numeric(28,10) must be skipped")
	}
	fitted, ok := fitQuotedPrice("SHIB", mustDecimal(t, "0.000012345678915"))
	if !ok {
		t.Fatal("a small positive quote must be kept")
	}
	requireDecimal(t, fitted, "0.0000123457")
}
