package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const quoteRecipientEVM = "0x00000000000000000000000000000000000000aa"

func quote(t *testing.T, svc *service, walletID uuid.UUID, asset string, amount int64, to string) *FeeQuote {
	t.Helper()
	q, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: walletID, Asset: asset, Amount: big.NewInt(amount), ToAddress: to})
	if err != nil {
		t.Fatalf("QuoteWithdrawalFee: %v", err)
	}
	return q
}

func requireBigInt(t *testing.T, name string, got *big.Int, want int64) {
	t.Helper()
	if got == nil || got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

// ---------------------------------------------------------------------------
// EVM native
// ---------------------------------------------------------------------------

func TestQuoteEVMNative_DirectMatchesThePlanAndTheReserve(t *testing.T) {
	balance := new(big.Int).Add(big.NewInt(evmNativeAmount), evmNativeFee)
	svc, fixture, _ := evmNativePlanner(t, balance, false)

	q := quote(t, svc, fixture.wallet.ID, gasPlanNative, evmNativeAmount, quoteRecipientEVM)
	plan := planEVMNative(t, svc, fixture, big.NewInt(evmNativeAmount))

	if q.Fee.Cmp(plan.EstimatedGas) != 0 || q.Fee.Cmp(evmNativeFee) != 0 {
		t.Fatalf("fee %s, plan %s, reserve %s: all three must agree", q.Fee, plan.EstimatedGas, evmNativeFee)
	}
	if q.Basis != FeeBasisPlan || q.Strategy != StrategyDirectFromBase || !q.AmountSpendable || q.Transfers != 1 {
		t.Fatalf("quote %+v", q)
	}
	if q.EVM == nil || q.EVM.GasLimit != 21_000 || q.EVM.GasPrice.Int64() != 2_000_000_000 || q.EVM.L1DataFee.Sign() != 0 {
		t.Fatalf("details %+v", q.EVM)
	}
	if q.FeeAsset != gasPlanNative || q.MinimumRemaining.Sign() != 0 {
		t.Fatalf("fee asset %s minimum %s", q.FeeAsset, q.MinimumRemaining)
	}
}

func TestQuoteEVMNative_InsufficientStillQuotesTheFeeAndFlagsIt(t *testing.T) {
	svc, fixture, _ := evmNativePlanner(t, big.NewInt(evmNativeAmount), false)

	q := quote(t, svc, fixture.wallet.ID, gasPlanNative, evmNativeAmount, quoteRecipientEVM)

	if q.Strategy != StrategyInsufficient || q.Basis != FeeBasisUnfundedDirect || q.AmountSpendable {
		t.Fatalf("quote %+v", q)
	}
	if q.Fee.Cmp(evmNativeFee) != 0 {
		t.Fatalf("fee %s, want the direct transfer's %s", q.Fee, evmNativeFee)
	}
}

func TestQuote_RejectsNonPositiveAmounts(t *testing.T) {
	svc, fixture, _ := evmNativePlanner(t, big.NewInt(evmNativeAmount), false)
	for _, amount := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1)} {
		if _, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: fixture.wallet.ID, Asset: gasPlanNative, Amount: amount}); err == nil {
			t.Fatalf("amount %v must be refused", amount)
		}
	}
}

// ---------------------------------------------------------------------------
// EVM ERC-20 (USDC on Polygon: the fee is in the native coin)
// ---------------------------------------------------------------------------

// erc20QuoteFee is FakeEVMNode's 79012 gas estimate padded by 25% at 2 gwei.
var erc20QuoteFee = big.NewInt(98_765 * 2_000_000_000)

func TestQuoteERC20_MatchesTheBuiltTransactionAndThePlan(t *testing.T) {
	fixture := newEVMGasFixture(t)
	svc := fixture.plannerService()
	amount := big.NewInt(3_000_000)

	q := quote(t, svc, fixture.wallet.ID, gasPlanAsset, amount.Int64(), gasPlanDestination)

	unsigned, err := fixture.adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasPlanBase, To: gasPlanDestination, Amount: amount, Asset: gasPlanAsset, Token: &gasPlanToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	price, _ := new(big.Int).SetString(unsigned.Metadata["gas_price"].(string), 10)
	built := new(big.Int).Mul(price, new(big.Int).SetUint64(unsigned.Metadata["gas_limit"].(uint64)))
	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanAsset, amount, gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Fee.Cmp(built) != 0 || q.Fee.Cmp(plan.EstimatedGas) != 0 || q.Fee.Cmp(erc20QuoteFee) != 0 {
		t.Fatalf("fee %s, built tx %s, plan %s", q.Fee, built, plan.EstimatedGas)
	}
	if q.FeeAsset != gasPlanNative || q.Asset != gasPlanAsset || q.EVM.GasLimit != 98_765 || q.RecipientIsProbe {
		t.Fatalf("quote %+v details %+v", q, q.EVM)
	}
}

func TestQuoteERC20_WithoutRecipientSimulatesTheProbe(t *testing.T) {
	fixture := newEVMGasFixture(t)
	svc := fixture.plannerService()

	q := quote(t, svc, fixture.wallet.ID, gasPlanAsset, 3_000_000, "")

	if !q.RecipientIsProbe || q.Fee.Cmp(erc20QuoteFee) != 0 {
		t.Fatalf("quote %+v", q)
	}
	probe := strings.ToLower(strings.TrimPrefix(chain.EVMFeeProbeRecipient(), "0x"))
	calls := fixture.node.CallsTo("eth_estimateGas")
	if len(calls) == 0 || !strings.Contains(strings.ToLower(string(calls[len(calls)-1].Params[0])), probe) {
		t.Fatalf("the token transfer must be simulated to the probe %s: %+v", probe, calls)
	}
}

func TestQuoteERC20_UnfundedAmountIsSimulatedAtTheBaseBalance(t *testing.T) {
	fixture := newEVMGasFixture(t) // 20 USDC on base
	svc := fixture.plannerService()

	q := quote(t, svc, fixture.wallet.ID, gasPlanAsset, 30_000_000, gasPlanDestination)

	if q.Strategy != StrategyInsufficient || q.Basis != FeeBasisUnfundedDirect || q.AmountSpendable || q.Fee.Cmp(erc20QuoteFee) != 0 {
		t.Fatalf("quote %+v", q)
	}
	requireBigInt(t, "quoted amount", q.Amount, 30_000_000)
	calls := fixture.node.CallsTo("eth_estimateGas")
	cappedAmountWord := strings.Repeat("0", 64-len(big.NewInt(20_000_000).Text(16))) + big.NewInt(20_000_000).Text(16)
	if !strings.Contains(string(calls[len(calls)-1].Params[0]), cappedAmountWord) {
		t.Fatalf("the simulation must transfer the 20 USDC the base holds: %s", calls[len(calls)-1].Params[0])
	}
}

func TestQuoteERC20_NoTokenBalanceCannotBeSimulated(t *testing.T) {
	fixture := newEVMGasFixture(t)
	fixture.node.TokenBalanceHex = "0x0"
	svc := fixture.plannerService()

	_, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: fixture.wallet.ID, Asset: gasPlanAsset, Amount: big.NewInt(1_000_000), ToAddress: gasPlanDestination})
	if !errors.Is(err, ErrFeeQuoteNeedsTokenBalance) {
		t.Fatalf("err %v", err)
	}
}

func TestQuoteERC20_RevertingTransferFailsWithoutAGuess(t *testing.T) {
	fixture := newEVMGasFixture(t)
	fixture.node.EstimateGasError = "execution reverted: blacklisted"
	svc := fixture.plannerService()

	_, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: fixture.wallet.ID, Asset: gasPlanAsset, Amount: big.NewInt(1_000_000), ToAddress: gasPlanDestination})
	if !errors.Is(err, ErrGasEstimateFailed) {
		t.Fatalf("err %v", err)
	}
}

func TestQuoteERC20_UnusableGasPriceIsUnavailable(t *testing.T) {
	fixture := newEVMGasFixture(t)
	fixture.node.GasPriceHex = "0x0"
	svc := fixture.plannerService()

	_, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: fixture.wallet.ID, Asset: gasPlanAsset, Amount: big.NewInt(1_000_000), ToAddress: gasPlanDestination})
	if !errors.Is(err, ErrFeeQuoteUnavailable) {
		t.Fatalf("err %v", err)
	}
}

// ---------------------------------------------------------------------------
// EVM L2: OP-stack L1 data fee, and multi-sweep agreeing with the planner
// ---------------------------------------------------------------------------

func TestQuoteBaseSepolia_AddsTheL1DataFee(t *testing.T) {
	balance := big.NewInt(l2FeeTestAmount + l2FeeTestNativeGas + l2FeeTestBufferedL1Fee)
	svc, walletID := baseSepoliaPlanner(t, balance)

	q := quote(t, svc, walletID, models.NativeETH, l2FeeTestAmount, gasPlanDestination)

	requireBigInt(t, "fee", q.Fee, l2FeeTestNativeGas+l2FeeTestBufferedL1Fee)
	requireBigInt(t, "l1 data fee", q.EVM.L1DataFee, l2FeeTestBufferedL1Fee)
	if q.EVM.GasLimit != 21_000 || !q.AmountSpendable {
		t.Fatalf("quote %+v details %+v", q, q.EVM)
	}
}

func TestQuoteMultiSweep_EqualsThePlannersEstimate(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	childB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	balances := balanceMapChain(models.ChainArbitrum, models.NativeETH, map[string]*big.Int{
		"BASE": big.NewInt(0), "CHILD_A": big.NewInt(300), "CHILD_B": big.NewInt(250),
	})
	balances.EstimateGasPriceVal = big.NewInt(10_000_000)
	balances.ValidateAddressFn = func(string) bool { return true }
	perLeg := map[string]uint64{"CHILD_A": 70_000, "CHILD_B": 80_000, "BASE": 90_000}
	rollup := &l1FeeMockChain{
		estimatingMockChain: &estimatingMockChain{MockChain: balances, estimate: func(req types.TransferRequest) (uint64, error) {
			return perLeg[req.From], nil
		}},
		l1Fee:        big.NewInt(1_000),
		seedGasLimit: 32_000,
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(rollup)
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: &models.Wallet{ID: walletID, Chain: models.ChainArbitrum, DepositAddress: &baseAddr}},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr, childA, childB}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity(models.ChainArbitrum)},
	}

	q := quote(t, svc, walletID, models.NativeETH, 500, "DEST")
	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeETH, big.NewInt(500), "DEST", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}

	if q.Strategy != StrategyMultiSweep || q.Fee.Cmp(plan.EstimatedGas) != 0 || q.Transfers != 5 {
		t.Fatalf("quote %+v, plan estimate %s", q, plan.EstimatedGas)
	}
	if q.EVM.GasLimit != 2*32_000+70_000+80_000+90_000 {
		t.Fatalf("gas limit %d", q.EVM.GasLimit)
	}
	requireBigInt(t, "l1 data fee", q.EVM.L1DataFee, 5*1_000)
}

// ---------------------------------------------------------------------------
// Bitcoin (fake bitcoind at 2 sat/vB)
// ---------------------------------------------------------------------------

func TestQuoteBitcoin_DirectUsesTheBuildersSelection(t *testing.T) {
	svc, walletID, _ := btcPlanner(t, map[string][]fakeUTXO{"tb1qbase": {{sats: 100_000, confirmations: 3}}})

	q := quote(t, svc, walletID, models.NativeBTC, 10_000, "tb1qdest")

	requireBigInt(t, "fee", q.Fee, 141*btcPlannerSatPerVByte)
	want := BitcoinFeeDetails{MilliSatPerVByte: 2_000, VSize: 141, Inputs: 1, Outputs: 2}
	if q.Bitcoin == nil || *q.Bitcoin != want || q.Transfers != 1 || !q.AmountSpendable {
		t.Fatalf("quote %+v details %+v", q, q.Bitcoin)
	}
}

func TestQuoteBitcoin_MultiSweepAddsTheLegFeeAndSpendsItsOutput(t *testing.T) {
	svc, walletID, _ := btcPlanner(t, map[string][]fakeUTXO{
		"tb1qbase":  {{sats: 50_000, confirmations: 1}},
		"tb1qchild": {{sats: 40_000, confirmations: 1}, {sats: 40_000, confirmations: 1}, {sats: 70_000, confirmations: 0}},
	}, "tb1qchild")

	q := quote(t, svc, walletID, models.NativeBTC, 100_000, "tb1qdest")

	// leg: 2 inputs → 1 output (178 vB); final: base utxo + swept output → payment + change (209 vB).
	if q.Strategy != StrategyMultiSweep || q.Transfers != 2 {
		t.Fatalf("quote %+v", q)
	}
	requireBigInt(t, "fee", q.Fee, (178+209)*btcPlannerSatPerVByte)
	if q.Bitcoin.VSize != 178+209 || q.Bitcoin.Inputs != 2 || q.Bitcoin.Outputs != 2 {
		t.Fatalf("details %+v", q.Bitcoin)
	}
}

func TestQuoteBitcoin_InsufficientQuotesATypicalTransfer(t *testing.T) {
	svc, walletID, _ := btcPlanner(t, map[string][]fakeUTXO{"tb1qbase": {{sats: 10_000, confirmations: 1}}})

	q := quote(t, svc, walletID, models.NativeBTC, 500_000, "")

	if q.Strategy != StrategyInsufficient || q.Basis != FeeBasisUnfundedDirect || q.AmountSpendable {
		t.Fatalf("quote %+v", q)
	}
	requireBigInt(t, "fee", q.Fee, 141*btcPlannerSatPerVByte)
}

// ---------------------------------------------------------------------------
// Solana (fake RPC)
// ---------------------------------------------------------------------------

const (
	quoteSOLBase     = "7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV"
	quoteSOLDest     = "HasJJjmZYYiQXu1qBgRCuffdCrH4S8yGtwYXLhHR2mWH"
	quoteSOLRent0    = 890_880
	quoteSOLRent165  = 2_039_280
	quoteSOLLamports = 5_000
)

var quoteSOLUSDC = types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCMintSOL, Decimals: 6, ChainID: models.ChainSOL}

func solanaQuotePlanner(t *testing.T, results map[string]string) (*service, uuid.UUID) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		key := req.Method
		if req.Method == "getMinimumBalanceForRentExemption" {
			key += ":" + string(req.Params[0])
		}
		result, ok := results[key]
		if !ok {
			t.Errorf("unexpected solana rpc %s", key)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	adapter := chain.NewSolanaLive(chain.SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL})
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(quoteSOLUSDC)
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: quoteSOLBase}
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: &models.Wallet{ID: walletID, Chain: models.ChainSOL, DepositAddress: &base}},
		addressRepo: &fakeAddressRepo{children: []models.Address{base}},
		chainRepo:   &fakeChainRepo{chain: &models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana}},
	}, walletID
}

func TestQuoteSolanaNative_OneSignatureAndTheRentMinimum(t *testing.T) {
	svc, walletID := solanaQuotePlanner(t, map[string]string{
		"getBalance":                          `"result":{"value":29995000}`,
		"getMinimumBalanceForRentExemption:0": `"result":890880`,
	})

	q := quote(t, svc, walletID, models.NativeSOL, 20_000_000, quoteSOLDest)

	requireBigInt(t, "fee", q.Fee, quoteSOLLamports)
	requireBigInt(t, "minimum remaining", q.MinimumRemaining, quoteSOLRent0)
	if q.Solana == nil || q.Solana.Signatures != 1 || q.Solana.LamportsPerSignature != quoteSOLLamports || !q.AmountSpendable {
		t.Fatalf("quote %+v details %+v", q, q.Solana)
	}
}

func TestQuoteSolanaNative_InsufficientKeepsTheFee(t *testing.T) {
	svc, walletID := solanaQuotePlanner(t, map[string]string{
		"getBalance":                          `"result":{"value":20000000}`,
		"getMinimumBalanceForRentExemption:0": `"result":890880`,
	})

	q := quote(t, svc, walletID, models.NativeSOL, 20_000_000, "")

	if q.AmountSpendable || q.Basis != FeeBasisUnfundedDirect {
		t.Fatalf("20 000 000 lamports cannot leave the fee and the rent minimum: %+v", q)
	}
	requireBigInt(t, "fee", q.Fee, quoteSOLLamports)
}

func TestQuoteSolanaSPL_FundsTheMissingRecipientTokenAccount(t *testing.T) {
	svc, walletID := solanaQuotePlanner(t, map[string]string{
		"getTokenAccountBalance":                `"result":{"value":{"amount":"5000000","decimals":6}}`,
		"getAccountInfo":                        `"result":{"context":{"slot":1},"value":null}`,
		"getMinimumBalanceForRentExemption:165": `"result":2039280`,
	})

	q := quote(t, svc, walletID, models.SymbolUSDC, 1_000_000, quoteSOLDest)

	requireBigInt(t, "fee", q.Fee, quoteSOLLamports+quoteSOLRent165)
	requireBigInt(t, "account creation", q.Solana.AccountCreationLamports, quoteSOLRent165)
	if q.FeeAsset != models.NativeSOL || q.MinimumRemaining.Sign() != 0 {
		t.Fatalf("quote %+v", q)
	}
}

func TestQuoteSolanaSPL_RentFailureIsUnavailable(t *testing.T) {
	svc, walletID := solanaQuotePlanner(t, map[string]string{
		"getTokenAccountBalance":                `"result":{"value":{"amount":"5000000","decimals":6}}`,
		"getAccountInfo":                        `"result":{"context":{"slot":1},"value":null}`,
		"getMinimumBalanceForRentExemption:165": `"error":{"code":-32000,"message":"node behind"}`,
	})

	_, err := svc.QuoteWithdrawalFee(context.Background(), FeeQuoteRequest{WalletID: walletID, Asset: models.SymbolUSDC, Amount: big.NewInt(1_000_000), ToAddress: quoteSOLDest})
	if !errors.Is(err, ErrFeeQuoteUnavailable) {
		t.Fatalf("err %v", err)
	}
}
