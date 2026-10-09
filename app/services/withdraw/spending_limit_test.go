package withdraw

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/tests/memcache"
)

func TestStore_Spending_LimitBlankAndNonNegative(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value map[string]any
		want  string
	}{
		{name: "nil", want: "{}"},
		{name: "empty object", value: map[string]any{}, want: "{}"},
		{name: "blank daily usd", value: map[string]any{"daily_usd": "  "}, want: "{}"},
		{name: "null daily usd", value: map[string]any{"daily_usd": nil}, want: "{}"},
		{name: "zero", value: map[string]any{"daily_usd": "0"}, want: `{"daily_usd":"0"}`},
		{name: "decimal string", value: map[string]any{"daily_usd": "12.50"}, want: `{"daily_usd":"12.50"}`},
		{name: "json number", value: map[string]any{"daily_usd": 10.5}, want: `{"daily_usd":"10.5"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := StoreSpendingLimit(tc.value)
			if err != nil {
				t.Fatalf("store: %v", err)
			}
			if got != tc.want {
				t.Fatalf("stored %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStore_Spending_LimitRejectsANegativeAmount(t *testing.T) {
	t.Parallel()
	cases := []map[string]any{
		{"daily_usd": "-1"},
		{"daily_usd": "-0.01"},
		{"daily_usd": float64(-2)},
		{"daily_usd": "10", "amount": float64(-1)},
	}
	for _, value := range cases {
		got, err := StoreSpendingLimit(value)
		if !errors.Is(err, ErrNegativeSpendingLimit) {
			t.Fatalf("store %#v err = %v", value, err)
		}
		if got != "" {
			t.Fatalf("negative amount was stored as %s", got)
		}
	}
}

func TestBlank_Spending_LimitDoesNotTouchRedisOrTheQuote(t *testing.T) {
	cache := memcache.New()
	quote := &fixedQuote{usd: decimal.NewFromInt(1), fail: errors.New("quote must not run")}
	svc := &Service{cache: cache, usdQuote: quote}
	for _, stored := range []string{"", "{}", "null", `{"daily_usd":""}`, `{"daily_usd":null}`} {
		err := svc.enforceTokenSpendingLimit(context.Background(), WithdrawRequest{
			AccessTokenID: uuid.New(),
			SpendingLimit: stored,
			Asset:         "USDC",
			QuoteAmount:   "5",
		})
		if err != nil {
			t.Fatalf("blank %s: %v", stored, err)
		}
	}
	if keys := cache.Keys(); len(keys) != 0 {
		t.Fatalf("blank cap touched the cache: %v", keys)
	}
	if quote.calls != 0 {
		t.Fatalf("blank cap quoted %d times", quote.calls)
	}
}

func TestDaily_USD_CapIsPerTokenAndRefundsARejectedSpend(t *testing.T) {
	tokenID := uuid.New()
	otherID := uuid.New()
	cache := memcache.New()
	quote := &scriptedQuote{usd: []decimal.Decimal{
		decimal.NewFromInt(60), decimal.NewFromInt(50), decimal.NewFromInt(30), decimal.NewFromInt(10),
	}}
	svc := &Service{cache: cache, usdQuote: quote}
	req := WithdrawRequest{
		AccessTokenID: tokenID,
		SpendingLimit: `{"daily_usd":"100"}`,
		Asset:         "USDC",
		QuoteAmount:   "1",
	}

	if err := svc.enforceTokenSpendingLimit(context.Background(), req); err != nil {
		t.Fatalf("first spend: %v", err)
	}
	err := svc.enforceTokenSpendingLimit(context.Background(), req)
	if !errors.Is(err, ErrSpendingLimitExceeded) {
		t.Fatalf("second spend: %v", err)
	}
	if err := svc.enforceTokenSpendingLimit(context.Background(), req); err != nil {
		t.Fatalf("third spend after refund: %v", err)
	}
	other := req
	other.AccessTokenID = otherID
	if err := svc.enforceTokenSpendingLimit(context.Background(), other); err != nil {
		t.Fatalf("other token: %v", err)
	}

	day := time.Now().UTC().Format("2006-01-02")
	key := spendingLimitKey(tokenID, time.Now().UTC())
	if !strings.Contains(key, tokenID.String()) || !strings.HasSuffix(key, day) {
		t.Fatalf("key %s is not the token's UTC day", key)
	}
	if got := cache.GetInt64(key); got != 9000 {
		t.Fatalf("token counter = %d cents, want 9000", got)
	}
	otherKey := spendingLimitKey(otherID, time.Now().UTC())
	if got := cache.GetInt64(otherKey); got != 1000 {
		t.Fatalf("other token counter = %d cents, want 1000", got)
	}
	if quote.assets[0] != "USDC" || !quote.amounts[0].Equal(decimal.NewFromInt(1)) {
		t.Fatalf("quote saw asset %s amount %v", quote.assets[0], quote.amounts[0])
	}
}

func TestSet_Cap_WithoutAPriceStopsTheWithdrawal(t *testing.T) {
	svc := &Service{cache: memcache.New(), usdQuote: failingQuote{}}
	err := svc.enforceTokenSpendingLimit(context.Background(), WithdrawRequest{
		AccessTokenID: uuid.New(),
		SpendingLimit: `{"daily_usd":"10"}`,
		Asset:         "ETH",
		QuoteAmount:   "0.1",
	})
	if !errors.Is(err, ErrSpendingQuoteUnavailable) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("quote failure leaked the upstream error")
	}
}

func TestStored_Negative_CapIsNotTreatedAsUnlimited(t *testing.T) {
	err := (&Service{cache: memcache.New(), usdQuote: &fixedQuote{usd: decimal.NewFromInt(1)}}).enforceTokenSpendingLimit(
		context.Background(),
		WithdrawRequest{
			AccessTokenID: uuid.New(),
			SpendingLimit: `{"daily_usd":"-5"}`,
			Asset:         "USDC",
			QuoteAmount:   "1",
		},
	)
	if !errors.Is(err, ErrSpendingLimitInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestRequest_Over_TheDailyCapDoesNotNeedAWallet(t *testing.T) {
	cache := memcache.New()
	tokenID := uuid.New()
	svc := &Service{cache: cache, usdQuote: &fixedQuote{usd: decimal.NewFromInt(25)}}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase:    "validpassphrase123",
		WalletID:      uuid.New(),
		AccessTokenID: tokenID,
		SpendingLimit: `{"daily_usd":"10"}`,
		Asset:         "USDC",
		QuoteAmount:   "25",
	})
	if !errors.Is(err, ErrSpendingLimitExceeded) {
		t.Fatalf("got %v", err)
	}
	if got := cache.GetInt64(spendingLimitKey(tokenID, time.Now().UTC())); got != 0 {
		t.Fatalf("rejected spend stayed reserved: %d", got)
	}
}

func TestRequest_Blank_CapStillReportsABusyWallet(t *testing.T) {
	cache := memcache.New()
	walletID := uuid.New()
	cache.Add("vault:lock:withdrawal:"+walletID.String(), 1, time.Minute)
	svc := &Service{cache: cache, usdQuote: failingQuote{}}
	_, _, err := svc.Request(context.Background(), WithdrawRequest{
		Passphrase:    "validpassphrase123",
		WalletID:      walletID,
		SpendingLimit: "{}",
		Asset:         "USDC",
		QuoteAmount:   "1",
	})
	if !errors.Is(err, ErrConcurrentWithdraw) {
		t.Fatalf("got %v", err)
	}
	if got := cache.Keys(); len(got) != 1 {
		t.Fatalf("blank cap wrote a counter next to the held lock: %v", got)
	}
}

type fixedQuote struct {
	usd    decimal.Decimal
	fail   error
	calls  int
	asset  string
	amount decimal.Decimal
}

func (f *fixedQuote) ConvertToUSD(_ context.Context, code string, amount decimal.Decimal) (decimal.Decimal, error) {
	f.calls++
	f.asset = code
	f.amount = amount
	if f.fail != nil {
		return decimal.Decimal{}, f.fail
	}
	return f.usd, nil
}

type scriptedQuote struct {
	usd     []decimal.Decimal
	index   int
	assets  []string
	amounts []decimal.Decimal
}

func (s *scriptedQuote) ConvertToUSD(_ context.Context, code string, amount decimal.Decimal) (decimal.Decimal, error) {
	s.assets = append(s.assets, code)
	s.amounts = append(s.amounts, amount)
	usd := s.usd[s.index]
	s.index++
	return usd, nil
}

type failingQuote struct{}

func (failingQuote) ConvertToUSD(context.Context, string, decimal.Decimal) (decimal.Decimal, error) {
	return decimal.Decimal{}, errors.New("dial price provider secret-key: timeout")
}
