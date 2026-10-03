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
	"github.com/macrowallets/waas/tests/mocks"
)

func setFeeMultiplier(t *testing.T, wallet *models.Wallet, multiplier string) {
	t.Helper()
	wallet.FeeMultiplier = numeric.NewNullDecimal(decimal.RequireFromString(multiplier))
}

func TestQuoteAndPlanPriceWithTheWalletFeeMultiplier(t *testing.T) {
	balance := new(big.Int).Add(big.NewInt(evmNativeAmount), new(big.Int).Mul(evmNativeFee, big.NewInt(3)))
	svc, fixture, _ := evmNativePlanner(t, balance, false)

	defaultQuote := quote(t, svc, fixture.wallet.ID, gasPlanNative, evmNativeAmount, quoteRecipientEVM)
	if defaultQuote.Fee.Cmp(evmNativeFee) != 0 || !defaultQuote.FeeMultiplier.Equal(models.FeeMultiplierMin) {
		t.Fatalf("NULL multiplier: fee %s ×%s, want %s ×1", defaultQuote.Fee, defaultQuote.FeeMultiplier, evmNativeFee)
	}

	setFeeMultiplier(t, fixture.wallet, "2.5")
	scaled := quote(t, svc, fixture.wallet.ID, gasPlanNative, evmNativeAmount, quoteRecipientEVM)
	plan := planEVMNative(t, svc, fixture, big.NewInt(evmNativeAmount))

	wantFee := new(big.Int).Div(new(big.Int).Mul(evmNativeFee, big.NewInt(25)), big.NewInt(10))
	if scaled.Fee.Cmp(wantFee) != 0 || plan.EstimatedGas.Cmp(wantFee) != 0 {
		t.Fatalf("×2.5: quote %s, plan %s, want %s", scaled.Fee, plan.EstimatedGas, wantFee)
	}
	if !scaled.FeeMultiplier.Equal(decimal.RequireFromString("2.5")) || scaled.EVM.GasPrice.Int64() != 5_000_000_000 {
		t.Fatalf("quote multiplier %s gas price %s", scaled.FeeMultiplier, scaled.EVM.GasPrice)
	}

	unsigned, err := mustScopedAdapter(t, svc, fixture.wallet).BuildTransfer(context.Background(), types.TransferRequest{
		From: gasPlanBase, To: quoteRecipientEVM, Amount: big.NewInt(evmNativeAmount), Asset: gasPlanNative,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsigned.Metadata["gas_price"] != scaled.EVM.GasPrice.String() {
		t.Fatalf("the withdrawal bids %v, the quote priced %s", unsigned.Metadata["gas_price"], scaled.EVM.GasPrice)
	}
}

func TestQuoteRefusesAStoredMultiplierOutOfRange(t *testing.T) {
	balance := new(big.Int).Add(big.NewInt(evmNativeAmount), evmNativeFee)
	svc, fixture, _ := evmNativePlanner(t, balance, false)
	fixture.wallet.FeeMultiplier = numeric.NewNullDecimal(decimal.NewFromInt(50))

	_, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{
		WalletID: fixture.wallet.ID, Asset: gasPlanNative, Amount: big.NewInt(evmNativeAmount), ToAddress: quoteRecipientEVM,
	})
	if !errors.Is(err, models.ErrFeeMultiplierOutOfRange) {
		t.Fatalf("err %v, want ErrFeeMultiplierOutOfRange", err)
	}
}

func mustScopedAdapter(t *testing.T, svc *service, wallet *models.Wallet) types.Chain {
	t.Helper()
	adapter, err := svc.registry.ChainForWallet(wallet)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// scopingMockChain hands out a distinct adapter for a wallet fee policy, so a
// test can tell which one the executor built the withdrawal with.
type scopingMockChain struct {
	*mocks.MockChain
	scoped *mocks.MockChain
}

func (c *scopingMockChain) WithFeePolicy(chain.FeePolicy) types.Chain { return c.scoped }

var errBuiltWithScopedAdapter = errors.New("built with the wallet-scoped adapter")

func TestExecutePlanBuildsWithTheWalletScopedAdapter(t *testing.T) {
	shared := sweepMockChain(models.ChainETH, models.NativeETH)
	shared.BuildTransferFn = func(context.Context, types.TransferRequest) (*types.UnsignedTx, error) {
		return nil, errors.New("built with the shared adapter")
	}
	scoped := sweepMockChain(models.ChainETH, models.NativeETH)
	scoped.BuildTransferFn = func(context.Context, types.TransferRequest) (*types.UnsignedTx, error) {
		return nil, errBuiltWithScopedAdapter
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(&scopingMockChain{MockChain: shared, scoped: scoped})

	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: gasPlanBase}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainETH, DepositAddress: &base, MPCCurve: "secp256k1"}
	setFeeMultiplier(t, wallet, "1.5")
	svc := &service{
		registry:   registry,
		walletRepo: &fakeWalletRepo{wallet: wallet},
		txRepo:     &fakeTxRepo{},
		fetchShareBFn: func(context.Context, *models.Wallet) ([]byte, error) {
			return []byte("share-b"), nil
		},
	}
	plan := &Plan{WalletID: walletID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &base, ReachesTarget: true}

	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{ShareA: []byte("share-a")}, uuid.New(), gasPlanDestination, "")
	if !errors.Is(err, errBuiltWithScopedAdapter) {
		t.Fatalf("err %v, want the build to go through the wallet-scoped adapter", err)
	}

	plan.Chain = models.ChainSOL
	if _, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, ""); err == nil {
		t.Fatal("a plan on another chain than the wallet must be refused")
	}
}
