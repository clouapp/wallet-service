package chain

import (
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
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

func TestFee_Policy_ForWalletReadsTheStoredSettings(t *testing.T) {
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

func TestFee_Policy_ForWalletChecksTheFeeRateBounds(t *testing.T) {
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

func TestScale_Gas_PriceRoundsUpExactly(t *testing.T) {
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

func TestAdjust_Milli_SatRateScalesAndClamps(t *testing.T) {
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

func TestFee_Policy_FingerprintSeparatesSettings(t *testing.T) {
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
