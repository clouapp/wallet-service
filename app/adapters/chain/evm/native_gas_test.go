package evm

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// Sepolia, 2026-10-18: eth_estimateGas of 0.002 ETH from the withdrawal sender to
// an account that does not exist yet answered 0x32a1f (207,391 gas); to an
// existing account, 0x5208 (21,000). The withdrawal built with the fixed 21000
// (0x235681c8…26cb) was mined with status 0x0, out of gas.
const (
	sepoliaAccountCreationGasHex = "0x32a1f"
	sepoliaWithdrawalSender      = "0xe3c1d504c4633521197ffa736b2c9a7f93478076"
	sepoliaFreshRecipient        = "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a"
	sepoliaExistingRecipient     = gasTestTo
	sepoliaWithdrawalValueHex    = "0x71afd498d0000" // 0.002 ETH
	// sepoliaFreshLimit is 207,391 × 120 %; sepoliaExistingLimit is 21,000 × 120 %.
	sepoliaFreshLimit    = uint64(248_869)
	sepoliaExistingLimit = uint64(25_200)
	oneEtherHex          = "0xde0b6b3a7640000"
)

var sepoliaWithdrawalAmount = big.NewInt(2_000_000_000_000_000)

func newSepoliaNode(t *testing.T) *mocks.FakeEVMNode {
	t.Helper()
	node := mocks.NewFakeEVMNode(t)
	node.AccountCreationGasHex = sepoliaAccountCreationGasHex
	node.ExistingAccounts = []string{sepoliaWithdrawalSender, sepoliaExistingRecipient, gasTestFrom}
	node.NativeBalanceHex = oneEtherHex
	return node
}

func newSepoliaAdapter(node *mocks.FakeEVMNode) *EVMLive {
	return newNetworkAdapter(node, models.ChainTETH, models.NativeETH, models.EVMNetworkIDEthereumSepolia)
}

func sepoliaWithdrawal(to string) types.TransferRequest {
	return types.TransferRequest{
		From: sepoliaWithdrawalSender, To: to, Amount: new(big.Int).Set(sepoliaWithdrawalAmount), Asset: models.NativeETH,
	}
}

func TestEVM_BuildTransfer_NativeToAFreshAccountEncodesThePaddedEstimate(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	unsigned, err := adapter.BuildTransfer(context.Background(), sepoliaWithdrawal(sepoliaFreshRecipient))
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if got := builtGasLimit(t, unsigned); got != sepoliaFreshLimit {
		t.Fatalf("gas_limit = %d, want 207391 × 120%% = %d (21000 runs out of gas creating the account)", got, sepoliaFreshLimit)
	}
	estimates := node.Estimates(t)
	if len(estimates) != 1 {
		t.Fatalf("expected one eth_estimateGas call, got %d", len(estimates))
	}
	got := estimates[0]
	if !strings.EqualFold(got.From, sepoliaWithdrawalSender) || !strings.EqualFold(got.To, sepoliaFreshRecipient) ||
		got.Value != sepoliaWithdrawalValueHex || got.Data != "" {
		t.Fatalf("the estimate must be the real transfer (from, to, value), got %+v", got)
	}
}

func TestEVM_BuildTransfer_NativeToAnExistingAccountUsesTheEstimateToo(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	unsigned, err := adapter.BuildTransfer(context.Background(), sepoliaWithdrawal(sepoliaExistingRecipient))
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	if got := builtGasLimit(t, unsigned); got != sepoliaExistingLimit {
		t.Fatalf("gas_limit = %d, want 21000 × 120%% = %d", got, sepoliaExistingLimit)
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 1 {
		t.Fatalf("an existing recipient is estimated as well, got %d eth_estimateGas calls", len(calls))
	}
}

func TestEVM_BuildTransfer_NativeRevertFromAFundedSenderFailsWithoutFallback(t *testing.T) {
	node := newSepoliaNode(t)
	node.EstimateGasError = "execution reverted"
	adapter := newSepoliaAdapter(node)

	unsigned, err := adapter.BuildTransfer(context.Background(), sepoliaWithdrawal(sepoliaFreshRecipient))
	if err == nil {
		t.Fatalf("expected an error, got gas_limit %v", unsigned.Metadata["gas_limit"])
	}
	if !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("expected chain.ErrGasEstimateFailed, got %v", err)
	}
	if strings.Contains(err.Error(), "execution reverted") || !strings.Contains(chain.CauseText(err), "execution reverted") {
		t.Fatalf("the node sentence belongs on the log cause, not Error(): %q / cause %q", err.Error(), chain.CauseText(err))
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 1 {
		t.Fatalf("a sender that holds the value is not re-estimated from another sender, got %d estimates", len(calls))
	}
	for _, method := range []string{"eth_getTransactionCount", "eth_sendRawTransaction"} {
		if calls := node.CallsTo(method); len(calls) != 0 {
			t.Fatalf("nothing may be built or broadcast after a failed estimate, got %d %s", len(calls), method)
		}
	}
}

func TestEVM_BuildTransfer_NativeEstimateOutageFailsRetryably(t *testing.T) {
	node := newSepoliaNode(t)
	node.EstimateGasHTTPStatus = 503
	adapter := newSepoliaAdapter(node)

	_, err := adapter.BuildTransfer(context.Background(), sepoliaWithdrawal(sepoliaExistingRecipient))
	if !errors.Is(err, chain.ErrGasEstimateFailed) || !errors.Is(err, chain.ErrProviderUnavailable) || !chain.KnownFailure(err) {
		t.Fatalf("an outage must fail the estimate as a known, retryable provider failure, got %v", err)
	}
	if calls := node.CallsTo("eth_estimateGas"); len(calls) != 1 {
		t.Fatalf("an outage is not retried with another sender, got %d estimates", len(calls))
	}
	if calls := node.CallsTo("eth_getBalance"); len(calls) != 0 {
		t.Fatalf("an outage needs no balance read, got %d", len(calls))
	}
	if calls := node.CallsTo("eth_sendRawTransaction"); len(calls) != 0 {
		t.Fatalf("nothing may be broadcast, got %d", len(calls))
	}
}

func TestEVM_EstimateTransferGasLimit_SizesASenderThatDoesNotHoldTheAmountFromTheDefaultSender(t *testing.T) {
	node := newSepoliaNode(t)
	node.EnforceSenderBalance = true
	node.NativeBalanceHex = "0x0" // a multi_sweep base before its sweeps land, or an unfunded quote
	adapter := newSepoliaAdapter(node)

	limit, err := adapter.EstimateTransferGasLimit(context.Background(), sepoliaWithdrawal(sepoliaFreshRecipient))
	if err != nil {
		t.Fatalf("EstimateTransferGasLimit: %v", err)
	}
	if limit != sepoliaFreshLimit {
		t.Fatalf("limit = %d, want %d", limit, sepoliaFreshLimit)
	}
	estimates := node.Estimates(t)
	if len(estimates) != 2 {
		t.Fatalf("expected the real estimate then one from the default sender, got %d", len(estimates))
	}
	real, sized := estimates[0], estimates[1]
	if !strings.EqualFold(real.From, sepoliaWithdrawalSender) || real.Value != sepoliaWithdrawalValueHex {
		t.Fatalf("the first estimate must be the real transfer, got %+v", real)
	}
	if sized.From != "" || sized.Value != "0x1" {
		t.Fatalf("the retry is sized from the node's default sender with 1 wei (which it surely holds), got %+v", sized)
	}
	for _, estimate := range estimates {
		if !strings.EqualFold(estimate.To, sepoliaFreshRecipient) {
			t.Fatalf("both estimates must keep the recipient, got %+v", estimate)
		}
	}
}

func TestEVM_NativeTransferGasLimit_SizesAnUnknownAmountWithAPositiveValue(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	limit, err := adapter.NativeTransferGasLimit(context.Background(), gasTestFrom, sepoliaFreshRecipient)
	if err != nil {
		t.Fatalf("NativeTransferGasLimit: %v", err)
	}
	if limit != sepoliaFreshLimit {
		t.Fatalf("a gas_seed to a fresh child needs %d, got %d", sepoliaFreshLimit, limit)
	}
	estimate := node.Estimates(t)[0]
	if estimate.Value != "0x1" || !strings.EqualFold(estimate.From, gasTestFrom) {
		t.Fatalf("a zero-value transfer creates no account; the sizing value must be positive, got %+v", estimate)
	}
}

func TestEVM_NativeTransferReserveTo_PricesTheRecipient(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	for _, tc := range []struct {
		name  string
		to    string
		limit uint64
	}{
		{"fresh account", sepoliaFreshRecipient, sepoliaFreshLimit},
		{"existing account", sepoliaExistingRecipient, sepoliaExistingLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fee, minimumRemaining, err := adapter.NativeTransferReserveTo(context.Background(), tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if want := bufferedL2Fee(tc.limit); fee.Cmp(want) != 0 {
				t.Fatalf("reserve %s wei, want %d gas × 2 gwei = %s", fee, tc.limit, want)
			}
			if minimumRemaining == nil || minimumRemaining.Sign() != 0 {
				t.Fatalf("EVM keeps no minimum balance, got %v", minimumRemaining)
			}
		})
	}
}

func TestEVM_NativeTransferReserveTo_AnUnknownRecipientIsPricedAsANewAccount(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	for _, to := range []string{"", chain.EVMFeeProbeRecipient()} {
		fee, _, err := adapter.NativeTransferReserveTo(context.Background(), to)
		if err != nil {
			t.Fatal(err)
		}
		if want := bufferedL2Fee(sepoliaFreshLimit); fee.Cmp(want) != 0 {
			t.Fatalf("recipient %q: reserve %s, want the account-creation cost %s", to, fee, want)
		}
	}
	estimates := node.Estimates(t)
	if len(estimates) != 2 {
		t.Fatalf("expected two estimates, got %d", len(estimates))
	}
	for _, estimate := range estimates {
		if !common.IsHexAddress(estimate.To) || strings.EqualFold(estimate.To, chain.EVMFeeProbeRecipient()) {
			t.Fatalf("an unknown recipient must be a fresh address, not the shared probe, got %q", estimate.To)
		}
		if estimate.From != "" || estimate.Value != "0x1" {
			t.Fatalf("the reserve is sized from the node's default sender with a positive value, got %+v", estimate)
		}
	}
	if strings.EqualFold(estimates[0].To, estimates[1].To) {
		t.Fatalf("each unknown recipient is a new address, got %s twice", estimates[0].To)
	}
}

func TestEVM_EstimateFee_NativeMatchesTheBuiltTransferToAFreshAccount(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)

	estimate, err := adapter.EstimateFee(context.Background(), sepoliaWithdrawal(sepoliaFreshRecipient))
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := adapter.BuildTransfer(context.Background(), sepoliaWithdrawal(sepoliaFreshRecipient))
	if err != nil {
		t.Fatal(err)
	}
	limit, price := builtGasLimit(t, unsigned), builtGasPrice(t, unsigned)
	if estimate.GasLimit != limit || limit != sepoliaFreshLimit || estimate.GasPrice != price.String() {
		t.Fatalf("estimate (limit %d, price %s) must match the built tx (limit %d, price %s)", estimate.GasLimit, estimate.GasPrice, limit, price)
	}
	if want := fmtUnits(new(big.Int).Mul(price, new(big.Int).SetUint64(limit)), 18); estimate.Fee != want {
		t.Fatalf("fee %s, want %s", estimate.Fee, want)
	}
}

func TestEVM_BuildSweep_NativeIntoAFreshBaseLeavesThePaddedEstimate(t *testing.T) {
	node := newSepoliaNode(t)
	adapter := newSepoliaAdapter(node)
	balance := big.NewInt(1_000_000_000_000_000)

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: gasTestFrom, To: sepoliaFreshRecipient, NativeBalance: balance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("a native sweep is one transfer, got %d", len(txs))
	}
	limit := builtGasLimit(t, &txs[0])
	if limit != sepoliaFreshLimit {
		t.Fatalf("gas_limit %d, want %d", limit, sepoliaFreshLimit)
	}
	value, _ := new(big.Int).SetString(txs[0].Metadata["value"].(string), 10)
	fee := new(big.Int).Mul(builtGasPrice(t, &txs[0]), new(big.Int).SetUint64(limit))
	if new(big.Int).Add(value, fee).Cmp(balance) != 0 {
		t.Fatalf("value %s + gas %s must equal the balance %s", value, fee, balance)
	}
}

func TestEVM_BuildSweep_TokenSeedsAFreshChildWithTheAccountCreationGas(t *testing.T) {
	node := newSepoliaNode(t)
	node.EstimateGasHex = "0x134a4" // the token transfer: 79,012 × 125 % = 98,765
	adapter := newSepoliaAdapter(node)
	const freshChild = sepoliaFreshRecipient

	txs, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From: freshChild, To: gasTestFrom, Asset: gasTestToken.Symbol,
		Amount: big.NewInt(3_000_000), NativeBalance: new(big.Int), Token: &gasTestToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("expected gas_seed + sweep, got %d txs", len(txs))
	}
	seed := txs[0]
	if got := builtGasLimit(t, &seed); got != sepoliaFreshLimit {
		t.Fatalf("gas_seed gas_limit %d, want %d: the seed creates the child's account", got, sepoliaFreshLimit)
	}
	var seedEstimate *mocks.FakeEVMEstimate
	for _, estimate := range node.Estimates(t) {
		if estimate.Data == "" {
			seedEstimate = &estimate
		}
	}
	if seedEstimate == nil {
		t.Fatal("the gas_seed was not estimated")
	}
	seedValue, _ := new(big.Int).SetString(seed.Metadata["value"].(string), 10)
	if !strings.EqualFold(seedEstimate.From, gasTestFrom) || !strings.EqualFold(seedEstimate.To, freshChild) ||
		seedEstimate.Value != "0x"+seedValue.Text(16) {
		t.Fatalf("the gas_seed estimate must be the real seed (base → child, its value), got %+v", seedEstimate)
	}
}
