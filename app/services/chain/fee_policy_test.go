package chain

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

func multiplierWallet(t *testing.T, chainID, multiplier string) *models.Wallet {
	t.Helper()
	wallet := &models.Wallet{ID: uuid.New(), Chain: chainID}
	if multiplier != "" {
		wallet.FeeMultiplier = numeric.NewNullDecimal(decimal.RequireFromString(multiplier))
	}
	return wallet
}

func intPointer(value int) *int { return &value }

func TestFeePolicyForWalletReadsTheStoredSettings(t *testing.T) {
	cases := []struct {
		name       string
		multiplier string
		wantFactor string
		wantErr    error
	}{
		{name: "NULL keeps the network fee", multiplier: "", wantFactor: "1"},
		{name: "1.0000 keeps the network fee", multiplier: "1.0000", wantFactor: "1"},
		{name: "1.25", multiplier: "1.25", wantFactor: "1.25"},
		{name: "upper bound", multiplier: "5", wantFactor: "5"},
		{name: "below the range", multiplier: "0.9999", wantErr: models.ErrFeeMultiplierOutOfRange},
		{name: "above the range", multiplier: "5.0001", wantErr: models.ErrFeeMultiplierOutOfRange},
		{name: "more decimals than the column", multiplier: "1.00001", wantErr: numeric.ErrTooManyDecimals},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := FeePolicyForWallet(multiplierWallet(t, models.ChainBase, tc.multiplier))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !policy.Multiplier().Equal(decimal.RequireFromString(tc.wantFactor)) {
				t.Fatalf("multiplier %s, want %s", policy.Multiplier(), tc.wantFactor)
			}
			if policy.IsDefault() != (tc.wantFactor == "1") {
				t.Fatalf("IsDefault %v for multiplier %s", policy.IsDefault(), tc.wantFactor)
			}
		})
	}
	if _, err := FeePolicyForWallet(nil); err == nil {
		t.Fatal("a nil wallet must be refused")
	}
}

func TestFeePolicyForWalletChecksTheFeeRateBounds(t *testing.T) {
	wallet := multiplierWallet(t, models.ChainBTC, "")
	wallet.FeeRateMin, wallet.FeeRateMax = intPointer(20), intPointer(10)
	if _, err := FeePolicyForWallet(wallet); !errors.Is(err, models.ErrFeeRateBoundsInverted) {
		t.Fatalf("inverted bounds: %v", err)
	}
	wallet.FeeRateMin, wallet.FeeRateMax = intPointer(0), nil
	if _, err := FeePolicyForWallet(wallet); !errors.Is(err, models.ErrFeeRateOutOfRange) {
		t.Fatalf("zero minimum: %v", err)
	}
	wallet.FeeRateMin, wallet.FeeRateMax = intPointer(2), intPointer(50)
	policy, err := FeePolicyForWallet(wallet)
	if err != nil || policy.IsDefault() || policy.minMilliSatPerVByte != 2_000 || policy.maxMilliSatPerVByte != 50_000 {
		t.Fatalf("policy %+v err %v", policy, err)
	}
}

func TestScaleGasPriceRoundsUpExactly(t *testing.T) {
	policy, err := NewFeePolicy(FeePolicyDeps{Multiplier: decimal.RequireFromString("1.25")})
	if err != nil {
		t.Fatal(err)
	}
	// 1 000 000 001 × 1.25 = 1 250 000 001.25 → 1 250 000 002 wei.
	if got := policy.scaleGasPrice(big.NewInt(1_000_000_001)); got.Cmp(big.NewInt(1_250_000_002)) != 0 {
		t.Fatalf("scaled %s", got)
	}
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	want, _ := new(big.Int).SetString("154320986265432098626543209863", 10) // ceil(×1.25)
	if got := policy.scaleGasPrice(huge); got.Cmp(want) != 0 {
		t.Fatalf("scaled %s, want %s", got, want)
	}
	if got := policy.scaleGasPrice(nil); got != nil {
		t.Fatalf("nil stays nil, got %s", got)
	}
	if got := policy.scaleGasPrice(new(big.Int)); got.Sign() != 0 {
		t.Fatalf("zero stays zero, got %s", got)
	}
	if got := (FeePolicy{}).scaleGasPrice(big.NewInt(7)); got.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("the default policy changes nothing, got %s", got)
	}
}

func TestAdjustMilliSatRateScalesAndClamps(t *testing.T) {
	multiplied := FeePolicy{multiplier: decimal.RequireFromString("1.5")}
	bounded := FeePolicy{multiplier: decimal.RequireFromString("3"), minMilliSatPerVByte: 2_000, maxMilliSatPerVByte: 10_000}
	cases := []struct {
		name   string
		policy FeePolicy
		rate   int64
		want   int64
	}{
		{"default keeps the rate", FeePolicy{}, 4_200, 4_200},
		{"multiplier rounds up", multiplied, 1_001, 1_502},
		{"minimum lifts a low rate", bounded, 500, 2_000},
		{"maximum caps a high rate", bounded, 4_000, 10_000},
		{"sanity ceiling", FeePolicy{multiplier: decimal.NewFromInt(5)}, 9_000_000, btcMaxSaneSatPerVByte * milliSatsPerSat},
		{"min relay floor", FeePolicy{maxMilliSatPerVByte: 500}, 3_000, btcMinRelayMilliSatPerVByte},
		{"no rate stays unset", multiplied, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.adjustMilliSatRate(tc.rate); got != tc.want {
				t.Fatalf("adjusted %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEVMGasPriceAndBuiltTransfersFollowTheWalletMultiplier(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	shared := newNetworkAdapter(node, models.ChainETH, models.NativeETH, models.EVMNetworkIDEthereumSepolia)
	policy, err := NewFeePolicy(FeePolicyDeps{Multiplier: decimal.RequireFromString("1.5")})
	if err != nil {
		t.Fatal(err)
	}
	scoped := shared.WithFeePolicy(policy).(*EVMLive)

	defaultPrice, err := shared.EstimateGasPrice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scaledPrice, err := scoped.EstimateGasPrice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1 gwei node price × 2 buffer = 2 gwei; × 1.5 = 3 gwei.
	if defaultPrice.Cmp(big.NewInt(2_000_000_000)) != 0 || scaledPrice.Cmp(big.NewInt(3_000_000_000)) != 0 {
		t.Fatalf("default %s, scaled %s", defaultPrice, scaledPrice)
	}

	unsigned, err := scoped.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1), Asset: models.NativeETH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if built := builtGasPrice(t, unsigned); built.Cmp(scaledPrice) != 0 {
		t.Fatalf("the transfer bids %s, the estimate priced %s", built, scaledPrice)
	}
	estimate, err := scoped.EstimateFee(context.Background(), types.TransferRequest{From: gasTestFrom, To: gasTestTo, Amount: big.NewInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	wantFee := new(big.Int).Mul(scaledPrice, new(big.Int).SetUint64(evmNativeTransferGasLimit))
	if estimate.Fee != fmtUnits(wantFee, 18) || estimate.GasPrice != scaledPrice.String() {
		t.Fatalf("estimate %+v, want fee %s at %s", estimate, fmtUnits(wantFee, 18), scaledPrice)
	}
	if scoped.FeePolicy().Multiplier().String() != "1.5" || !AppliedFeeMultiplier(shared).Equal(models.FeeMultiplierMin) {
		t.Fatalf("applied multipliers: scoped %s, shared %s", scoped.FeePolicy().Multiplier(), AppliedFeeMultiplier(shared))
	}
}

func TestEVMTokenSweepSeedsGasAtTheScaledPrice(t *testing.T) {
	adapter, node := newBaseSepoliaAdapter(t)
	node.EstimateGasHex = "0xc350"
	node.TokenBalanceHex = "0x" + big.NewInt(5_000_000).Text(16)
	policy, err := NewFeePolicy(FeePolicyDeps{Multiplier: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}

	txs, err := adapter.WithFeePolicy(policy).BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: gasTestTo, Token: &feeTestBaseUSDC, NativeBalance: new(big.Int),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("gas_seed + sweep expected, got %d", len(txs))
	}
	scaledPrice := big.NewInt(2 * evmGasPriceMultiplier * feeTestGasPrice)
	for index := range txs {
		if built := builtGasPrice(t, &txs[index]); built.Cmp(scaledPrice) != 0 {
			t.Fatalf("tx %d bids %s, want %s", index, built, scaledPrice)
		}
	}
	seed, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	l2Fee := new(big.Int).Mul(scaledPrice, new(big.Int).SetUint64(evmERC20TransferGasFloor))
	needed := l2Fee.Add(l2Fee, bufferedL1Fee())
	want := new(big.Int).Mul(needed, big.NewInt(evmGasSeedBufferPercent))
	want.Div(want, big.NewInt(percentDenominator))
	if seed.Cmp(want) != 0 {
		t.Fatalf("gas_seed %s, want %s", seed, want)
	}
}

func TestBitcoinRateFollowsTheWalletPolicyAndKeepsTheNetworkRateCached(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":4.2}`)
	shared := feeTestAdapter(esplora)
	policy, err := NewFeePolicy(FeePolicyDeps{Multiplier: decimal.RequireFromString("1.5")})
	if err != nil {
		t.Fatal(err)
	}
	scoped := shared.WithFeePolicy(policy).(*BitcoinLive)

	if got := scoped.feePolicy(context.Background()).milliSatPerVByte; got != 6_300 {
		t.Fatalf("scoped rate %d, want 4200 × 1.5 = 6300", got)
	}
	if got := shared.feePolicy(context.Background()).milliSatPerVByte; got != 4_200 {
		t.Fatalf("shared rate %d, want the network's 4200", got)
	}
	if hits := esplora.hitCount(esploraTestPrefix + "/fee-estimates"); hits != 1 {
		t.Fatalf("the scoped and shared adapters must share the rate cache, fetched %d times", hits)
	}

	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 40_000), utxo(1, 40_000)}, true))
	req := types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(60_000)}
	quote, err := scoped.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(60_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := scoped.BuildTransfer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if built := unsigned.Metadata["fee"].(int64); built != quote.Fee || quote.MilliSatPerVByte != 6_300 {
		t.Fatalf("quote %+v, built fee %d", quote, built)
	}
}

func TestBitcoinFlatFallbackFollowsTheWalletPolicy(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.on(esploraTestPrefix+"/fee-estimates", esploraAnswer{http.StatusInternalServerError, "boom"})
	policy, err := NewFeePolicy(FeePolicyDeps{Multiplier: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	scoped := feeTestAdapter(esplora).WithFeePolicy(policy).(*BitcoinLive)
	got := scoped.feePolicy(context.Background())
	if got.milliSatPerVByte != 0 || got.flatFee != int64(btcFeeVBytes*btcDefaultFeeRate*2) {
		t.Fatalf("flat fallback %+v, want %d sats", got, btcFeeVBytes*btcDefaultFeeRate*2)
	}
}

func TestChainForWalletScopesOnlyAdaptersWithPerUnitFees(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	registry := NewRegistry()
	evm := newNetworkAdapter(node, models.ChainETH, models.NativeETH, models.EVMNetworkIDEthereumSepolia)
	registry.RegisterChain(evm)
	registry.RegisterChain(mocks.NewMockChain(models.ChainSOL))

	shared, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, ""))
	if err != nil || shared != types.Chain(evm) {
		t.Fatalf("a default wallet gets the shared adapter, got %T err %v", shared, err)
	}
	scoped, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, "2"))
	if err != nil || scoped == types.Chain(evm) || !AppliedFeeMultiplier(scoped).Equal(decimal.NewFromInt(2)) {
		t.Fatalf("a wallet with a multiplier gets a scoped adapter, got %T err %v", scoped, err)
	}
	solana, err := registry.ChainForWallet(multiplierWallet(t, models.ChainSOL, "2"))
	if err != nil || !AppliedFeeMultiplier(solana).Equal(models.FeeMultiplierMin) {
		t.Fatalf("adapters without per-unit fees ignore the multiplier, got %T err %v", solana, err)
	}
	if _, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, "9")); !errors.Is(err, models.ErrFeeMultiplierOutOfRange) {
		t.Fatalf("a stored multiplier out of range must stop the withdrawal, got %v", err)
	}
	if _, err := registry.ChainForWallet(nil); err == nil {
		t.Fatal("a nil wallet must be refused")
	}
}

func TestFeePolicyFingerprintSeparatesSettings(t *testing.T) {
	defaultPrint := FeePolicyFingerprint(multiplierWallet(t, models.ChainETH, ""))
	if defaultPrint != FeePolicyFingerprint(multiplierWallet(t, models.ChainETH, "1.0")) {
		t.Fatal("NULL and 1.0 price identically and must share a fingerprint")
	}
	if defaultPrint == FeePolicyFingerprint(multiplierWallet(t, models.ChainETH, "1.5")) {
		t.Fatal("a multiplier must change the fingerprint")
	}
	if FeePolicyFingerprint(multiplierWallet(t, models.ChainETH, "1.5")) != FeePolicyFingerprint(multiplierWallet(t, models.ChainETH, "1.50")) {
		t.Fatal("1.5 and 1.50 are the same multiplier")
	}
}
