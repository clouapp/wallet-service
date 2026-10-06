package evm

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

func TestEVM_Gas_PriceAndBuiltTransfersFollowTheWalletMultiplier(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	shared := newNetworkAdapter(node, models.ChainETH, models.NativeETH, models.EVMNetworkIDEthereumSepolia)
	policy, err := chain.NewFeePolicy(chain.FeePolicyDeps{Multiplier: decimal.RequireFromString("1.5")})
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
	if scoped.FeePolicy().Multiplier().String() != "1.5" || !chain.AppliedFeeMultiplier(shared).Equal(models.FeeMultiplierMin) {
		t.Fatalf("applied multipliers: scoped %s, shared %s", scoped.FeePolicy().Multiplier(), chain.AppliedFeeMultiplier(shared))
	}
}

func TestEVM_Token_SweepSeedsGasAtTheScaledPrice(t *testing.T) {
	adapter, node := newBaseSepoliaAdapter(t)
	node.EstimateGasHex = "0xc350"
	node.TokenBalanceHex = "0x" + big.NewInt(5_000_000).Text(16)
	policy, err := chain.NewFeePolicy(chain.FeePolicyDeps{Multiplier: decimal.NewFromInt(2)})
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

func TestChain_For_WalletScopesOnlyAdaptersWithPerUnitFees(t *testing.T) {
	node := mocks.NewFakeEVMNode(t)
	registry := chain.NewRegistry()
	evm := newNetworkAdapter(node, models.ChainETH, models.NativeETH, models.EVMNetworkIDEthereumSepolia)
	registry.RegisterChain(evm)
	registry.RegisterChain(mocks.NewMockChain(models.ChainSOL))

	shared, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, ""))
	if err != nil || shared != types.Chain(evm) {
		t.Fatalf("a default wallet gets the shared adapter, got %T err %v", shared, err)
	}
	scoped, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, "2"))
	if err != nil || scoped == types.Chain(evm) || !chain.AppliedFeeMultiplier(scoped).Equal(decimal.NewFromInt(2)) {
		t.Fatalf("a wallet with a multiplier gets a scoped adapter, got %T err %v", scoped, err)
	}
	solana, err := registry.ChainForWallet(multiplierWallet(t, models.ChainSOL, "2"))
	if err != nil || !chain.AppliedFeeMultiplier(solana).Equal(models.FeeMultiplierMin) {
		t.Fatalf("adapters without per-unit fees ignore the multiplier, got %T err %v", solana, err)
	}
	if _, err := registry.ChainForWallet(multiplierWallet(t, models.ChainETH, "9")); !errors.Is(err, models.ErrFeeMultiplierOutOfRange) {
		t.Fatalf("a stored multiplier out of range must stop the withdrawal, got %v", err)
	}
	if _, err := registry.ChainForWallet(nil); err == nil {
		t.Fatal("a nil wallet must be refused")
	}
}
