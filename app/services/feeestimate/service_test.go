package feeestimate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	testEVMRecipient = "0x53bc147071251db8294b55a303a4570dab595178"
	testBTCDust      = 546
)

var (
	testNow    = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	testUSDC   = types.Token{Symbol: models.SymbolUSDC, Contract: "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", Decimals: 6, ChainID: models.ChainPolygon}
	sepoliaFee = big.NewInt(41_893_278_336_000)
)

type fakeQuoter struct {
	quote    *sweep.FeeQuote
	err      error
	requests []sweep.FeeQuoteRequest
}

func (f *fakeQuoter) QuoteWithdrawalFee(_ context.Context, req sweep.FeeQuoteRequest) (*sweep.FeeQuote, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	copied := *f.quote
	copied.Amount = req.Amount
	return &copied, nil
}

type fakeRegistry struct {
	chains map[string]types.Chain
	tokens map[string][]types.Token
}

func (r *fakeRegistry) Chain(id string) (types.Chain, error) {
	if adapter, ok := r.chains[id]; ok {
		return adapter, nil
	}
	return nil, fmt.Errorf("chain %s not registered", id)
}

func (r *fakeRegistry) TokensForChain(chainID string) []types.Token { return r.tokens[chainID] }

type fakeCatalog map[string]*models.Chain

func (c fakeCatalog) FindByID(id string) (*models.Chain, error) {
	if row, ok := c[id]; ok {
		return row, nil
	}
	return nil, errors.New("record not found")
}

type memoryCache struct {
	entries  map[string][]byte
	ttls     map[string]time.Duration
	getErr   error
	setErr   error
	setCalls int
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (c *memoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	if c.getErr != nil {
		return nil, false, c.getErr
	}
	value, ok := c.entries[key]
	return value, ok, nil
}

func (c *memoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.setCalls++
	if c.setErr != nil {
		return c.setErr
	}
	c.entries[key] = value
	c.ttls[key] = ttl
	return nil
}

type dustChain struct{ *mocks.MockChain }

func (dustChain) MinimumTransferAmount() *big.Int { return big.NewInt(testBTCDust) }

func evmAdapter(id, native string) *mocks.MockChain {
	adapter := mocks.NewMockChain(id)
	adapter.NativeAssetVal = native
	adapter.ValidateAddressFn = func(address string) bool {
		return len(address) == 42 && strings.HasPrefix(address, "0x")
	}
	return adapter
}

type fixture struct {
	service *Service
	quoter  *fakeQuoter
	cache   *memoryCache
	wallets map[string]*models.Wallet
}

func newFixture(t *testing.T, ttl time.Duration) *fixture {
	t.Helper()
	btc := mocks.NewMockChain(models.ChainBTC)
	btc.NativeAssetVal = models.NativeBTC
	btc.ValidateAddressFn = func(address string) bool { return strings.HasPrefix(address, "tb1") }
	registry := &fakeRegistry{
		chains: map[string]types.Chain{
			models.ChainETH:     evmAdapter(models.ChainETH, models.NativeETH),
			models.ChainPolygon: evmAdapter(models.ChainPolygon, models.NativePOL),
			models.ChainBTC:     dustChain{btc},
		},
		tokens: map[string][]types.Token{models.ChainPolygon: {testUSDC}},
	}
	catalog := fakeCatalog{
		models.ChainETH:     {ID: models.ChainETH, NativeDecimals: 18, AdapterType: models.AdapterTypeEVM},
		models.ChainPolygon: {ID: models.ChainPolygon, NativeDecimals: 18, AdapterType: models.AdapterTypeEVM},
		models.ChainBTC:     {ID: models.ChainBTC, NativeDecimals: 8, AdapterType: models.AdapterTypeBitcoin},
	}
	quoter := &fakeQuoter{quote: sepoliaQuote()}
	cache := newMemoryCache()
	service, err := NewService(quoter, registry, catalog, cache, ttl, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	wallets := map[string]*models.Wallet{}
	for _, chainID := range []string{models.ChainETH, models.ChainPolygon, models.ChainBTC, "unknown"} {
		wallets[chainID] = &models.Wallet{ID: uuid.New(), Chain: chainID}
	}
	return &fixture{service: service, quoter: quoter, cache: cache, wallets: wallets}
}

func sepoliaQuote() *sweep.FeeQuote {
	return &sweep.FeeQuote{
		Chain: models.ChainETH, Asset: models.NativeETH, FeeAsset: models.NativeETH,
		Fee: new(big.Int).Set(sepoliaFee), Strategy: sweep.StrategyDirectFromBase, Basis: sweep.FeeBasisPlan,
		AmountSpendable: true, Transfers: 1,
		BaseBalance: big.NewInt(1_958_106_721_664_000), MinimumRemaining: big.NewInt(0),
		EVM: &sweep.EVMFeeDetails{GasLimit: 21_000, GasPrice: big.NewInt(1_994_918_016), L1DataFee: big.NewInt(0)},
	}
}

func (f *fixture) estimate(t *testing.T, req Request) *Estimate {
	t.Helper()
	estimate, err := f.service.Estimate(context.Background(), req)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	return estimate
}

func requireError(t *testing.T, err error, kind Kind, code string) *Error {
	t.Helper()
	var estimateErr *Error
	if !errors.As(err, &estimateErr) {
		t.Fatalf("err %v (%T), want *Error %s", err, err, code)
	}
	if estimateErr.Kind != kind || estimateErr.Code != code {
		t.Fatalf("err kind %s code %s, want %s %s (%v)", estimateErr.Kind, estimateErr.Code, kind, code, err)
	}
	return estimateErr
}

func TestEstimate_RendersTheSepoliaWithdrawalFee(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)

	got := f.estimate(t, Request{Wallet: f.wallets[models.ChainETH], Asset: "eth", Amount: "0.001", To: testEVMRecipient})

	if got.Fee != "0.000041893278336" || got.FeeBaseUnits != "41893278336000" || got.FeeAsset != "ETH" || got.FeeDecimals != 18 {
		t.Fatalf("fee %s (%s) %s/%d", got.Fee, got.FeeBaseUnits, got.FeeAsset, got.FeeDecimals)
	}
	if got.Amount != "0.001" || got.AmountBaseUnits != "1000000000000000" || got.AmountIsReference || got.Asset != "ETH" {
		t.Fatalf("amount %s (%s) reference %t asset %s", got.Amount, got.AmountBaseUnits, got.AmountIsReference, got.Asset)
	}
	if got.Details.EVM == nil || got.Details.EVM.GasPriceGwei != "1.994918016" || got.Details.EVM.GasLimit != 21_000 || got.Details.Bitcoin != nil {
		t.Fatalf("details %+v", got.Details)
	}
	if !got.AmountSpendable || got.InsufficientFunds || got.Recipient != recipientProvided || got.Cached {
		t.Fatalf("flags %+v", got)
	}
	if !got.EstimatedAt.Equal(testNow) || !got.ExpiresAt.Equal(testNow.Add(DefaultCacheTTL)) {
		t.Fatalf("estimated %s expires %s", got.EstimatedAt, got.ExpiresAt)
	}
	request := f.quoter.requests[0]
	if request.Asset != models.NativeETH || request.Amount.String() != "1000000000000000" || request.ToAddress != testEVMRecipient {
		t.Fatalf("quote request %+v", request)
	}
}

func TestEstimate_TokenIsResolvedAndTheFeeStaysNative(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	f.quoter.quote.Chain, f.quoter.quote.Asset, f.quoter.quote.FeeAsset = models.ChainPolygon, models.SymbolUSDC, models.NativePOL

	got := f.estimate(t, Request{Wallet: f.wallets[models.ChainPolygon], Asset: "usdc", Amount: "1.5"})

	if f.quoter.requests[0].Asset != models.SymbolUSDC || f.quoter.requests[0].Amount.Int64() != 1_500_000 {
		t.Fatalf("quote request %+v", f.quoter.requests[0])
	}
	if got.Asset != models.SymbolUSDC || got.FeeAsset != models.NativePOL || got.Amount != "1.5" {
		t.Fatalf("estimate %+v", got)
	}
}

func TestEstimate_OmittedAmountQuotesTheReferenceMinimum(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)

	eth := f.estimate(t, Request{Wallet: f.wallets[models.ChainETH]})
	btc := f.estimate(t, Request{Wallet: f.wallets[models.ChainBTC], Asset: models.NativeBTC})

	if !eth.AmountIsReference || eth.AmountBaseUnits != "1" {
		t.Fatalf("eth reference %+v", eth)
	}
	if !btc.AmountIsReference || btc.AmountBaseUnits != "546" {
		t.Fatalf("btc reference must be the dust limit: %+v", btc)
	}
}

func TestEstimate_InvalidInputsFailBeforeQuoting(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	eth := f.wallets[models.ChainETH]
	cases := []struct {
		name string
		req  Request
		kind Kind
		code string
	}{
		{"missing wallet", Request{}, KindInvalidInput, CodeInvalidAmount},
		{"zero", Request{Wallet: eth, Amount: "0"}, KindInvalidInput, CodeInvalidAmount},
		{"negative", Request{Wallet: eth, Amount: "-1"}, KindInvalidInput, CodeInvalidAmount},
		{"not a number", Request{Wallet: eth, Amount: "abc"}, KindInvalidInput, CodeInvalidAmount},
		{"too many decimals", Request{Wallet: f.wallets[models.ChainPolygon], Asset: "USDC", Amount: "1.0000001"}, KindInvalidInput, CodeInvalidAmount},
		{"unknown asset", Request{Wallet: eth, Asset: "DOGE", Amount: "1"}, KindUnprocessable, CodeUnknownAsset},
		{"token of another chain", Request{Wallet: eth, Asset: "USDC", Amount: "1"}, KindUnprocessable, CodeUnknownAsset},
		{"invalid address", Request{Wallet: eth, Amount: "1", To: "tb1qnotevm"}, KindUnprocessable, CodeInvalidAddress},
		{"below dust", Request{Wallet: f.wallets[models.ChainBTC], Amount: "0.00000545"}, KindUnprocessable, CodeAmountBelowMinimum},
		{"unconfigured chain", Request{Wallet: f.wallets["unknown"], Amount: "1"}, KindUnprocessable, CodeUnsupportedChain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.service.Estimate(context.Background(), tc.req)
			requireError(t, err, tc.kind, tc.code)
		})
	}
	if len(f.quoter.requests) != 0 {
		t.Fatalf("invalid inputs reached the planner: %+v", f.quoter.requests)
	}
}

func TestEstimate_InsufficientFundsStillReturnsTheFee(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	f.quoter.quote.Strategy, f.quoter.quote.Basis, f.quoter.quote.AmountSpendable = sweep.StrategyInsufficient, sweep.FeeBasisUnfundedDirect, false

	got := f.estimate(t, Request{Wallet: f.wallets[models.ChainETH], Amount: "100"})

	if !got.InsufficientFunds || got.AmountSpendable || got.FeeBaseUnits != sepoliaFee.String() || got.Basis != string(sweep.FeeBasisUnfundedDirect) {
		t.Fatalf("estimate %+v", got)
	}
}

func TestEstimate_NeverRendersNegativeAmounts(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	f.quoter.quote.BaseBalance = big.NewInt(-5)
	f.quoter.quote.MinimumRemaining = nil
	f.quoter.quote.EVM.L1DataFee = big.NewInt(-1)

	got := f.estimate(t, Request{Wallet: f.wallets[models.ChainETH], Amount: "1"})

	if got.BaseBalanceBaseUnits != "0" || got.MinimumRemainingBaseUnits != "0" || got.Details.EVM.L1DataFeeWei != "0" {
		t.Fatalf("estimate %+v details %+v", got, got.Details.EVM)
	}
}

func TestEstimate_QuoteErrorsAreClassified(t *testing.T) {
	cases := map[error]struct {
		kind Kind
		code string
	}{
		sweep.ErrFeeQuoteUnavailable:                      {KindUnavailable, CodeUnavailable},
		fmt.Errorf("rpc: %w", sweep.ErrGasEstimateFailed): {KindUnavailable, CodeGasEstimateFailed},
		errors.New("dial tcp: connection refused"):        {KindUnavailable, CodeUnavailable},
		sweep.ErrFeeQuoteNeedsTokenBalance:                {KindUnprocessable, CodeTokenBalanceRequired},
		fmt.Errorf("plan: %w", sweep.ErrTooManyAddresses): {KindRateLimited, CodeTooManyAddresses},
		fmt.Errorf("plan: %w", sweep.ErrUnsupportedChain): {KindUnprocessable, CodeUnsupportedChain},
	}
	for quoteErr, want := range cases {
		t.Run(want.code+"/"+quoteErr.Error(), func(t *testing.T) {
			f := newFixture(t, DefaultCacheTTL)
			f.quoter.err = quoteErr

			_, err := f.service.Estimate(context.Background(), Request{Wallet: f.wallets[models.ChainETH], Amount: "1"})

			estimateErr := requireError(t, err, want.kind, want.code)
			if !errors.Is(estimateErr, quoteErr) {
				t.Fatal("the cause must stay unwrappable for logs")
			}
			if f.cache.setCalls != 0 {
				t.Fatal("errors must not be cached")
			}
		})
	}
}

func TestEstimate_CachesBrieflyPerWalletAssetAmountAndRecipient(t *testing.T) {
	f := newFixture(t, 20*time.Second)
	req := Request{Wallet: f.wallets[models.ChainETH], Amount: "0.001", To: testEVMRecipient}

	first := f.estimate(t, req)
	second := f.estimate(t, req)

	if len(f.quoter.requests) != 1 || first.Cached || !second.Cached || second.FeeBaseUnits != first.FeeBaseUnits {
		t.Fatalf("quotes %d first cached %t second cached %t", len(f.quoter.requests), first.Cached, second.Cached)
	}
	for key, ttl := range f.cache.ttls {
		if ttl != 20*time.Second || !strings.HasPrefix(key, cacheKeyPrefix+":"+req.Wallet.ID.String()+":default:ETH:1000000000000000:") {
			t.Fatalf("key %s ttl %s", key, ttl)
		}
	}

	f.estimate(t, Request{Wallet: req.Wallet, Amount: "0.002", To: testEVMRecipient})
	f.estimate(t, Request{Wallet: req.Wallet, Amount: "0.001"})
	f.estimate(t, Request{Wallet: f.wallets[models.ChainPolygon], Amount: "0.001", To: testEVMRecipient})
	if len(f.quoter.requests) != 4 {
		t.Fatalf("a different amount, recipient or wallet must not share an entry: %d quotes", len(f.quoter.requests))
	}
}

func TestEstimate_FeeMultiplierChangeNeverReusesACachedQuote(t *testing.T) {
	f := newFixture(t, 20*time.Second)
	wallet := f.wallets[models.ChainETH]
	req := Request{Wallet: wallet, Amount: "0.001", To: testEVMRecipient}

	f.estimate(t, req)
	wallet.FeeMultiplier = numeric.NewNullDecimal(decimal.RequireFromString("1.5"))
	afterChange := f.estimate(t, req)

	if len(f.quoter.requests) != 2 || afterChange.Cached {
		t.Fatalf("a new multiplier must be quoted afresh: %d quotes, cached %t", len(f.quoter.requests), afterChange.Cached)
	}
	for key := range f.cache.ttls {
		if strings.Contains(key, ":m1.5-") {
			return
		}
	}
	t.Fatalf("no cache key carries the multiplier fingerprint: %v", f.cache.ttls)
}

func TestEstimate_ZeroTTLDisablesTheCache(t *testing.T) {
	f := newFixture(t, 0)
	req := Request{Wallet: f.wallets[models.ChainETH], Amount: "1"}

	f.estimate(t, req)
	f.estimate(t, req)

	if len(f.quoter.requests) != 2 || f.cache.setCalls != 0 {
		t.Fatalf("quotes %d sets %d", len(f.quoter.requests), f.cache.setCalls)
	}
}

func TestEstimate_CacheFailuresFallThroughToAFreshQuote(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	f.cache.getErr = errors.New("redis down")
	f.cache.setErr = errors.New("redis down")

	got := f.estimate(t, Request{Wallet: f.wallets[models.ChainETH], Amount: "1"})

	if got.Cached || got.FeeBaseUnits != sepoliaFee.String() || len(f.quoter.requests) != 1 {
		t.Fatalf("estimate %+v", got)
	}
}

func TestEstimate_UnreadableCacheEntryIsAMiss(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	req := Request{Wallet: f.wallets[models.ChainETH], Amount: "1"}
	f.estimate(t, req)
	for key := range f.cache.entries {
		f.cache.entries[key] = []byte("{not json")
	}

	got := f.estimate(t, req)

	if got.Cached || len(f.quoter.requests) != 2 {
		t.Fatalf("cached %t quotes %d", got.Cached, len(f.quoter.requests))
	}
}

func TestEstimate_JSONShape(t *testing.T) {
	f := newFixture(t, DefaultCacheTTL)
	raw, err := json.Marshal(f.estimate(t, Request{Wallet: f.wallets[models.ChainETH], Amount: "0.001"}))
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"fee", "fee_base_units", "fee_asset", "insufficient_funds", "amount_spendable", "estimated_at", "expires_at", "details"} {
		if _, ok := shape[field]; !ok {
			t.Fatalf("missing %s in %s", field, raw)
		}
	}
	if _, ok := shape["details"].(map[string]any)["bitcoin"]; ok {
		t.Fatal("unused chain details must be omitted")
	}
}

func TestNewService_RequiresItsCollaborators(t *testing.T) {
	if _, err := NewService(nil, &fakeRegistry{}, fakeCatalog{}, nil, DefaultCacheTTL, nil); err == nil {
		t.Fatal("expected an error without a quoter")
	}
}

func TestRedisCache_NilClientIsANoOp(t *testing.T) {
	cache := NewRedisCache(nil)
	if _, ok, err := cache.Get(context.Background(), "k"); ok || err != nil {
		t.Fatalf("ok %t err %v", ok, err)
	}
	if err := cache.Set(context.Background(), "k", []byte("v"), time.Second); err != nil {
		t.Fatal(err)
	}
}
