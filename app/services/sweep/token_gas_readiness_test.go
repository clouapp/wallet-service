package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// This port models RPC observations separately for native and token balances.
// No test sends requests to a chain or touches a real wallet.
type tokenReadinessPort struct {
	*evmPlanPort
	tokens          map[string]*big.Int
	balanceErr      error
	priceErr        error
	l1Err           error
	l1FeeFn         func(types.TransferRequest) (*big.Int, error)
	missingBalance  bool
	requireHoldings bool
	balanceRead     func(address string) *big.Int
}

func (p *tokenReadinessPort) WithFeePolicy(policy chain.FeePolicy) types.Chain {
	p.fee = policy
	return p
}

func (p *tokenReadinessPort) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	if p.balanceErr != nil {
		return nil, p.balanceErr
	}
	if p.missingBalance {
		return nil, nil
	}
	if p.balanceRead != nil {
		return &types.Balance{Address: address, Amount: p.balanceRead(address)}, nil
	}
	return p.evmPlanPort.GetBalance(ctx, address)
}

func (p *tokenReadinessPort) GetTokenBalance(_ context.Context, address string, token types.Token) (*types.Balance, error) {
	amount := p.tokens[address]
	if amount == nil {
		amount = new(big.Int)
	}
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: new(big.Int).Set(amount)}, nil
}

func (p *tokenReadinessPort) EstimateGasPrice(ctx context.Context) (*big.Int, error) {
	if p.priceErr != nil {
		return nil, p.priceErr
	}
	return p.evmPlanPort.EstimateGasPrice(ctx)
}

func (p *tokenReadinessPort) EstimateTransferGasLimit(ctx context.Context, req types.TransferRequest) (uint64, error) {
	if p.requireHoldings && req.Token != nil {
		balance := p.tokens[req.From]
		if balance == nil || balance.Cmp(req.Amount) < 0 {
			return 0, chain.ErrGasEstimateFailed
		}
	}
	return p.evmPlanPort.EstimateTransferGasLimit(ctx, req)
}

func (p *tokenReadinessPort) EstimateL1DataFee(ctx context.Context, req types.TransferRequest) (*big.Int, error) {
	if p.l1Err != nil {
		return nil, p.l1Err
	}
	if p.l1FeeFn != nil {
		return p.l1FeeFn(req)
	}
	return p.evmPlanPort.EstimateL1DataFee(ctx, req)
}

func (p *tokenReadinessPort) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	p.BuildTransferCalls++
	return p.evmPlanPort.BuildTransfer(ctx, req)
}

func tokenReadinessFixture(t *testing.T, childGas, baseGas int64) (*service, *tokenReadinessPort, *models.Wallet, models.Address, *fakeTxRepo, *mocks.MockMPCService, *int) {
	t.Helper()
	port := &tokenReadinessPort{evmPlanPort: newEVMPlanPort(gasPlanChain, gasPlanNative, big.NewInt(7))}
	port.gasPrice, port.tokenGasLimit, port.nativeGasLimit = big.NewInt(2), 100, 10
	id := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: id}
	child := models.Address{ID: uuid.New(), WalletID: id}
	wallet := &models.Wallet{ID: id, Chain: gasPlanChain, DepositAddress: &base, MPCCurve: "secp256k1"}
	assignDerivedEVMAddresses(t, wallet, &base, &child)
	port.balances = map[string]*big.Int{base.Address: big.NewInt(baseGas), child.Address: big.NewInt(childGas)}
	port.tokens = map[string]*big.Int{base.Address: new(big.Int), child.Address: big.NewInt(11_000_000)}
	registry := chain.NewRegistry()
	registry.RegisterChain(port)
	registry.RegisterToken(gasPlanToken)
	mpc := mocks.NewMockMPCService()
	rows := &fakeTxRepo{}
	secretReads := new(int)
	svc := &service{
		registry: registry, mpc: mpc, walletRepo: &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{base, child}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity(gasPlanChain)}, txRepo: rows,
		fetchShareBFn: func(context.Context, *models.Wallet) ([]byte, error) {
			*secretReads++
			return []byte("synthetic-test-share"), nil
		},
	}
	return svc, port, wallet, child, rows, mpc, secretReads
}

func TestTokenGas_DirectChildRequiresItsOwnNativeFeeIncludingL1(t *testing.T) {
	for _, tc := range []struct {
		name     string
		childGas int64
		strategy Strategy
	}{
		{"exact_fee", 207, StrategyDirectFromChild},
		{"one_unit_short", 206, StrategyMultiSweep},
		{"zero_native", 0, StrategyMultiSweep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, port, wallet, child, _, _, _ := tokenReadinessFixture(t, tc.childGas, 10_000)
			plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
			if err != nil || plan == nil || plan.Strategy != tc.strategy {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if tc.strategy == StrategyDirectFromChild && (plan.SourceAddress == nil || plan.SourceAddress.ID != child.ID) {
				t.Fatalf("wrong direct sender: %+v", plan)
			}
			if port.BuildTransferCalls != 0 || port.BroadcastTransactionCalls != 0 {
				t.Fatal("planning built or broadcast a transaction")
			}
		})
	}
}

func TestTokenGas_PreparationBudgetsSeedBufferSeedFeeAndFinalFee(t *testing.T) {
	// Token fee 100*2+7=207; buffered seed 248; seed fee 10*2+7=27;
	// final token fee 207. The funder must cover 482 native units, not 207.
	for _, tc := range []struct {
		base int64
		ok   bool
	}{{481, false}, {482, true}, {0, false}} {
		svc, _, wallet, _, _, _, _ := tokenReadinessFixture(t, 0, tc.base)
		plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
		if tc.ok {
			if err != nil || plan == nil || plan.Strategy != StrategyMultiSweep {
				t.Fatalf("base %d: plan=%+v err=%v", tc.base, plan, err)
			}
		} else if !errors.Is(err, ErrWalletNotGasReady) || plan != nil {
			t.Fatalf("base %d must refuse: plan=%+v err=%v", tc.base, plan, err)
		}
	}
}

func TestTokenGas_L1FeeUsesTheExactSizedTokenAndSeedGasLimits(t *testing.T) {
	svc, port, wallet, child, _, _, _ := tokenReadinessFixture(t, 0, 690)
	var tokenQuotes, seedQuotes int
	port.l1FeeFn = func(req types.TransferRequest) (*big.Int, error) {
		if req.GasLimit == nil || req.GasPrice == nil || req.GasPrice.Int64() != 2 {
			t.Fatal("L1 quote must bind the actual gas limit and wallet gas price")
		}
		want := uint64(100)
		if req.Token == nil {
			want = 10
			seedQuotes++
			if req.From != wallet.DepositAddress.Address || req.To != child.Address {
				t.Fatal("seed quote changed funder or child")
			}
		} else {
			tokenQuotes++
		}
		if *req.GasLimit != want {
			t.Fatalf("L1 gas limit %d, want %d", *req.GasLimit, want)
		}
		return new(big.Int).SetUint64(*req.GasLimit), nil
	}
	plan := &Plan{WalletID: wallet.ID, Chain: wallet.Chain, Asset: gasPlanAsset, Amount: big.NewInt(6_100_000), Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: child, Amount: big.NewInt(11_000_000), NeedsGas: true}}}
	// Token fee 200+100=300; seed 360; seed fee 20+10=30;
	// final fee 300. Exactly 690 native units fund this preparation.
	if err := svc.requireTokenPlanGas(context.Background(), port, wallet, plan, gasPlanDestination); err != nil {
		t.Fatal(err)
	}
	port.balances[wallet.DepositAddress.Address] = big.NewInt(689)
	if err := svc.requireTokenPlanGas(context.Background(), port, wallet, plan, gasPlanDestination); !errors.Is(err, ErrWalletNotGasReady) {
		t.Fatalf("underfunded exact L1 budget: %v", err)
	}
	if tokenQuotes != 4 || seedQuotes != 2 {
		t.Fatalf("token quotes=%d seed quotes=%d", tokenQuotes, seedQuotes)
	}
}

func TestTokenGas_DirectBaseAlsoRequiresItsOwnGas(t *testing.T) {
	for _, balance := range []int64{0, 207} {
		svc, port, wallet, child, _, _, _ := tokenReadinessFixture(t, 0, balance)
		port.tokens[wallet.DepositAddress.Address] = big.NewInt(11_000_000)
		port.tokens[child.Address] = new(big.Int)
		plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
		if balance == 0 {
			if !errors.Is(err, ErrWalletNotGasReady) || plan != nil {
				t.Fatalf("unfunded base: plan=%+v err=%v", plan, err)
			}
		} else if err != nil || plan == nil || plan.Strategy != StrategyDirectFromBase {
			t.Fatalf("funded base: plan=%+v err=%v", plan, err)
		}
	}
}

func TestTokenGas_UnfundedPreparationStopsBeforeKeyRetrieval(t *testing.T) {
	svc, port, wallet, child, rows, mpc, secrets := tokenReadinessFixture(t, 0, 481)
	plan := &Plan{WalletID: wallet.ID, Chain: wallet.Chain, Asset: gasPlanAsset, Amount: big.NewInt(6_100_000), Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: child, Amount: big.NewInt(11_000_000), NeedsGas: true}}}
	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if !errors.Is(err, ErrWalletNotGasReady) {
		t.Fatalf("unfunded preparation: %v", err)
	}
	if *secrets != 0 || mpc.SignCalls != 0 || port.BroadcastTransactionCalls != 0 || len(rows.created) != 0 {
		t.Fatal("unfunded preparation touched keys, signing, persistence or broadcast")
	}
}

func TestTokenGas_RefusesUnestimableUnfundedBaseWithoutProjectedState(t *testing.T) {
	svc, port, wallet, _, rows, mpc, secrets := tokenReadinessFixture(t, 0, 10_000)
	port.requireHoldings = true
	plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
	if !errors.Is(err, ErrGasEstimateFailed) || plan != nil {
		t.Fatalf("unfunded base was incorrectly promised executable: plan=%+v err=%v", plan, err)
	}
	if *secrets != 0 || mpc.SignCalls != 0 || port.BroadcastTransactionCalls != 0 || len(rows.created) != 0 {
		t.Fatal("a refusal touched signing, persistence or broadcast")
	}
}

func TestTokenGas_ReadFailuresStopBeforeAnySigning(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*tokenReadinessPort)
	}{
		{"native_balance_rpc", func(p *tokenReadinessPort) { p.balanceErr = errors.New("balance unavailable") }},
		{"missing_native_balance", func(p *tokenReadinessPort) { p.missingBalance = true }},
		{"negative_native_balance", func(p *tokenReadinessPort) { p.balanceRead = func(string) *big.Int { return big.NewInt(-1) } }},
		{"gas_price_rpc", func(p *tokenReadinessPort) { p.priceErr = errors.New("fee unavailable") }},
		{"missing_gas_price", func(p *tokenReadinessPort) { p.gasPrice = nil }},
		{"zero_gas_price", func(p *tokenReadinessPort) { p.gasPrice = new(big.Int) }},
		{"gas_estimate_rpc", func(p *tokenReadinessPort) { p.estimateErr = chain.ErrGasEstimateFailed }},
		{"zero_gas_limit", func(p *tokenReadinessPort) { p.tokenGasLimit = 0 }},
		{"l1_fee_rpc", func(p *tokenReadinessPort) { p.l1Err = errors.New("L1 unavailable") }},
		{"negative_l1_fee", func(p *tokenReadinessPort) { p.l1Fee = big.NewInt(-1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, port, wallet, child, rows, mpc, secrets := tokenReadinessFixture(t, 1_000, 10_000)
			tc.set(port)
			plan := &Plan{WalletID: wallet.ID, Chain: wallet.Chain, Asset: gasPlanAsset, Amount: big.NewInt(6_100_000), Strategy: StrategyDirectFromChild, SourceAddress: &child}
			result, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
			if err == nil || result != nil {
				t.Fatalf("missing refusal: result=%+v err=%v", result, err)
			}
			if *secrets != 0 || mpc.SignCalls != 0 || port.BuildTransferCalls != 0 || port.BroadcastTransactionCalls != 0 || len(rows.created) != 0 {
				t.Fatal("readiness failure touched keys, signing, persistence or broadcast")
			}
		})
	}
}

func TestTokenGas_ExecutionRechecksAPlanWhoseChildLostGas(t *testing.T) {
	svc, port, wallet, child, rows, mpc, secrets := tokenReadinessFixture(t, 207, 10_000)
	plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
	if err != nil || plan.Strategy != StrategyDirectFromChild {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	port.balances[child.Address] = new(big.Int)
	_, err = svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if !errors.Is(err, ErrWalletNotGasReady) {
		t.Fatalf("unfunded execution was not refused: %v", err)
	}
	if *secrets != 0 || mpc.SignCalls != 0 || port.BroadcastTransactionCalls != 0 || len(rows.created) != 0 {
		t.Fatal("stale plan reached keys, signing or broadcast")
	}
}

func TestTokenGas_RechecksDirectSourceImmediatelyBeforeSigning(t *testing.T) {
	svc, port, wallet, child, rows, mpc, _ := tokenReadinessFixture(t, 207, 10_000)
	reads := 0
	port.balanceRead = func(string) *big.Int {
		reads++
		if reads == 1 {
			return big.NewInt(207)
		}
		return new(big.Int)
	}
	plan := &Plan{WalletID: wallet.ID, Chain: wallet.Chain, Asset: gasPlanAsset, Amount: big.NewInt(6_100_000), Strategy: StrategyDirectFromChild, SourceAddress: &child}
	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if !errors.Is(err, ErrWalletNotGasReady) || reads != 2 {
		t.Fatalf("expected two funding observations and refusal, reads=%d err=%v", reads, err)
	}
	if mpc.SignCalls != 0 || port.BuildTransferCalls != 0 || port.BroadcastTransactionCalls != 0 || len(rows.created) != 0 {
		t.Fatal("source whose gas disappeared reached signing or broadcast")
	}
}

func TestTokenGas_FundedDirectChildStillExecutesOneTransfer(t *testing.T) {
	svc, port, wallet, child, rows, mpc, secrets := tokenReadinessFixture(t, 207, 0)
	plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
	if err != nil || plan.Strategy != StrategyDirectFromChild {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	result, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if err != nil || result == nil || result.FinalWithdrawTx == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if *secrets != 1 || mpc.SignCalls != 1 || port.BroadcastTransactionCalls != 1 || port.BuildSweepCalls != 0 || len(rows.created) != 1 {
		t.Fatalf("funded direct path changed: keys=%d signs=%d sends=%d sweeps=%d rows=%d", *secrets, mpc.SignCalls, port.BroadcastTransactionCalls, port.BuildSweepCalls, len(rows.created))
	}
	if rows.created[0].FromAddress != child.Address {
		t.Fatal("direct execution changed the sender")
	}
}

func TestTokenGas_FundedPreparationUsesExistingSeedSweepAndFinalTransfer(t *testing.T) {
	svc, port, wallet, child, rows, mpc, _ := tokenReadinessFixture(t, 0, 482)
	port.GetTransactionBlockVal = 1
	port.BuildSweepFn = func(_ context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		if req.NativeBalance == nil || req.NativeBalance.Sign() != 0 {
			t.Fatal("preparation must observe the unfunded child")
		}
		return []types.UnsignedTx{{ChainID: wallet.Chain, RawBytes: []byte("seed")}, {ChainID: wallet.Chain, RawBytes: []byte("sweep")}}, nil
	}
	sends := 0
	port.BroadcastTransactionFn = func(context.Context, *types.SignedTx) (string, error) {
		sends++
		if sends == 1 {
			port.balances[child.Address] = big.NewInt(248)
			port.balances[wallet.DepositAddress.Address] = big.NewInt(207)
		}
		if sends == 2 {
			port.tokens[wallet.DepositAddress.Address] = big.NewInt(11_000_000)
		}
		return "synthetic-broadcast-hash", nil
	}
	plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
	if err != nil || plan.Strategy != StrategyMultiSweep {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	result, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if err != nil || result == nil || result.FinalWithdrawTx == nil || result.FailedStep != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if sends != 3 || mpc.SignCalls != 3 || port.BuildSweepCalls != 1 || len(rows.created) != 3 {
		t.Fatalf("expected seed + sweep + withdrawal; signs=%d sends=%d sweeps=%d rows=%d", mpc.SignCalls, sends, port.BuildSweepCalls, len(rows.created))
	}
	if rows.created[0].Origin != models.TxOriginGasSeed || rows.created[1].Origin != models.TxOriginSweep || rows.created[2].TxType != models.TxTypeWithdrawal {
		t.Fatal("existing preparation row types changed")
	}
}

func TestTokenGas_SeedInclusionWithoutObservedFundingNeverSignsTheChild(t *testing.T) {
	svc, port, wallet, _, rows, mpc, _ := tokenReadinessFixture(t, 0, 482)
	port.GetTransactionBlockVal = 1
	port.BuildSweepFn = func(context.Context, types.SweepRequest) ([]types.UnsignedTx, error) {
		return []types.UnsignedTx{{ChainID: wallet.Chain, RawBytes: []byte("seed")}, {ChainID: wallet.Chain, RawBytes: []byte("sweep")}}, nil
	}
	plan, err := svc.PlanForWithdrawal(context.Background(), wallet.ID, gasPlanAsset, big.NewInt(6_100_000), gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{}, uuid.New(), gasPlanDestination, "test-user")
	if err != nil || result == nil || result.FailedStep == nil || result.FinalWithdrawTx != nil {
		t.Fatalf("unfunded child must stop the preparation: result=%+v err=%v", result, err)
	}
	if mpc.SignCalls != 1 || port.BroadcastTransactionCalls != 1 || len(rows.created) != 1 || rows.created[0].Origin != models.TxOriginGasSeed {
		t.Fatal("unfunded child was signed or broadcast after the seed")
	}
}
