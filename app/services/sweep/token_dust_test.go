package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
)

const dustTestChain = models.ChainBase

var dustTestUSDC = types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCContractBaseSepolia, Decimals: 6, ChainID: dustTestChain}

type fakeTokenPricer struct {
	prices map[string]decimal.Decimal
	calls  int
}

func (p *fakeTokenPricer) QuotedUSDPrice(_ context.Context, code string) (decimal.Decimal, error) {
	p.calls++
	price, ok := p.prices[code]
	if !ok {
		return decimal.Decimal{}, errors.New("never quoted")
	}
	return price, nil
}

func usdcPricer(price string) *fakeTokenPricer {
	return &fakeTokenPricer{prices: map[string]decimal.Decimal{models.SymbolUSDC: decimal.RequireFromString(price)}}
}

func dustChainEntity(dustUSD string) *models.Chain {
	entity := &models.Chain{ID: dustTestChain, AdapterType: models.AdapterTypeEVM}
	if dustUSD != "" {
		entity.DustThresholdUSD = numeric.NewNullDecimal(decimal.RequireFromString(dustUSD))
	}
	return entity
}

func dustService(pricer TokenPricer) (*service, types.Chain) {
	adapter := balanceMapChain(dustTestChain, models.NativeETH, nil)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(dustTestUSDC)
	return &service{registry: registry, tokenPricer: pricer}, adapter
}

func TestDust_Base_UnitsRoundsUpToWholeBaseUnits(t *testing.T) {
	cases := []struct {
		dust, price string
		decimals    uint8
		want        string
	}{
		{"0.1", "1", 6, "100000"},
		{"0.1", "0.9998", 6, "100021"},       // 100 020.004… → 100 021
		{"1", "3000", 18, "333333333333334"}, // 333 333 333 333 333.33… → …334
		{"0.1", "2", 0, "1"},
	}
	for _, tc := range cases {
		got := dustBaseUnits(decimal.RequireFromString(tc.dust), decimal.RequireFromString(tc.price), tc.decimals)
		if got == nil || got.String() != tc.want {
			t.Fatalf("dust %s at %s (%d decimals) = %v, want %s", tc.dust, tc.price, tc.decimals, got, tc.want)
		}
	}
	if dustBaseUnits(decimal.Zero, decimal.NewFromInt(1), 6) != nil || dustBaseUnits(decimal.NewFromInt(1), decimal.Zero, 6) != nil {
		t.Fatal("a zero threshold or price filters nothing")
	}
}

func TestToken_Dust_UsesTheChainColumnAndIgnoresEnv(t *testing.T) {
	t.Setenv("BASE_DUST_THRESHOLD_USD", "3")
	svc, adapter := dustService(usdcPricer("1"))

	fromColumn := svc.childDustThreshold(context.Background(), adapter, dustChainEntity("0.5"), models.SymbolUSDC)
	if fromColumn == nil || fromColumn.Cmp(big.NewInt(500_000)) != 0 {
		t.Fatalf("column 0.5 USD → %v, want 500000", fromColumn)
	}
	fromUnset := svc.childDustThreshold(context.Background(), adapter, dustChainEntity(""), models.SymbolUSDC)
	if fromUnset != nil {
		t.Fatalf("unset column must not use BASE_DUST_THRESHOLD_USD, got %v", fromUnset)
	}
	if disabled := svc.childDustThreshold(context.Background(), adapter, dustChainEntity("0"), models.SymbolUSDC); disabled != nil {
		t.Fatalf("a zero column disables token dust, got %v", disabled)
	}
}

func TestToken_Dust_IsSkippedWithoutAQuotedPrice(t *testing.T) {
	svc, adapter := dustService(&fakeTokenPricer{prices: map[string]decimal.Decimal{}})
	if got := svc.childDustThreshold(context.Background(), adapter, dustChainEntity("0.1"), models.SymbolUSDC); got != nil {
		t.Fatalf("an unquoted price must not filter, got %v", got)
	}
	noPricer, adapter := dustService(nil)
	if got := noPricer.childDustThreshold(context.Background(), adapter, dustChainEntity("0.1"), models.SymbolUSDC); got != nil {
		t.Fatalf("no pricer must not filter, got %v", got)
	}
	unknown, adapter := dustService(usdcPricer("1"))
	if got := unknown.childDustThreshold(context.Background(), adapter, dustChainEntity("0.1"), "DAI"); got != nil {
		t.Fatalf("an unregistered token must not filter, got %v", got)
	}
}

func TestNative_Dust_KeepsTheAdapterThreshold(t *testing.T) {
	pricer := usdcPricer("1")
	svc, _ := dustService(pricer)
	adapter := balanceMapChain(dustTestChain, models.NativeETH, nil)
	adapter.DustThresholdFn = func(asset string) *big.Int {
		if asset == models.NativeETH {
			return big.NewInt(20_000_000_000_000)
		}
		return nil
	}
	got := svc.childDustThreshold(context.Background(), adapter, dustChainEntity("0.1"), models.NativeETH)
	if got == nil || got.Cmp(big.NewInt(20_000_000_000_000)) != 0 || pricer.calls != 0 {
		t.Fatalf("native dust %v (pricer calls %d), want the adapter's 20000000000000", got, pricer.calls)
	}
}

func TestPlanner_Ignores_TokenChildrenBelowTheUSDDust(t *testing.T) {
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	dusty := models.Address{ID: uuid.New(), WalletID: walletID, Address: "DUSTY"}
	funded := models.Address{ID: uuid.New(), WalletID: walletID, Address: "FUNDED"}
	wallet := &models.Wallet{ID: walletID, Chain: dustTestChain, DepositAddress: &base}

	adapter := balanceMapChain(dustTestChain, models.NativeETH, nil)
	tokenBalances := map[string]int64{"BASE": 0, "DUSTY": 90_000, "FUNDED": 400_000}
	adapter.GetTokenBalanceFn = func(_ context.Context, address string, token types.Token) (*types.Balance, error) {
		return &types.Balance{Address: address, Asset: token.Symbol, Amount: big.NewInt(tokenBalances[address])}, nil
	}
	svc := newPlannerService(t, wallet, []models.Address{base, dusty, funded}, adapter, dustChainEntity("0.1"))
	svc.registry.(*chain.Registry).RegisterToken(dustTestUSDC)
	svc.tokenPricer = usdcPricer("1")

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.SymbolUSDC, big.NewInt(300_000), "", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyDirectFromChild || plan.SourceAddress.Address != "FUNDED" {
		t.Fatalf("plan %s from %+v", plan.Strategy, plan.SourceAddress)
	}
	if len(plan.DustIgnored) != 1 || plan.DustIgnored[0].Address.Address != "DUSTY" || plan.DustIgnored[0].Reason != reasonBelowDustThreshold {
		t.Fatalf("dust ignored %+v, want DUSTY (0.09 USDC < 0.10 USD)", plan.DustIgnored)
	}

	consolidation, err := svc.planConsolidation(context.Background(), adapter, wallet, dustChainEntity("0.1"), models.SymbolUSDC, &Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if consolidation == nil || len(consolidation.Sweeps) != 1 || consolidation.Sweeps[0].From.Address != "FUNDED" {
		t.Fatalf("consolidation %+v, want one sweep from FUNDED", consolidation)
	}
}
