package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"

	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	gasPlanChain       = "polygon"
	gasPlanNative      = "matic"
	gasPlanAsset       = "USDC"
	gasPlanBase        = "0xdea40439a0736b4ec13dd28556e01465b8ceb39f"
	gasPlanDestination = "0x53bc147071251db8294b55a303a4570dab595178"
)

var gasPlanToken = types.Token{
	Symbol:   gasPlanAsset,
	Contract: "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
	Decimals: 6,
	ChainID:  gasPlanChain,
}

// estimatingMockChain adds TransferGasEstimator to MockChain so planner tests
// can drive per-leg gas estimates without an RPC node.
type estimatingMockChain struct {
	*mocks.MockChain
	estimate func(req types.TransferRequest) (uint64, error)
	requests []types.TransferRequest
}

func (m *estimatingMockChain) EstimateTransferGasLimit(_ context.Context, req types.TransferRequest) (uint64, error) {
	m.requests = append(m.requests, req)
	return m.estimate(req)
}

type evmGasFixture struct {
	node     *mocks.FakeEVMNode
	adapter  *evmchain.EVMLive
	registry *chain.Registry
	wallet   *models.Wallet
	base     models.Address
}

func newEVMGasFixture(t *testing.T) *evmGasFixture {
	t.Helper()
	node := mocks.NewFakeEVMNode(t)
	node.EstimateGasHex = "0x134a4"                               // 79_012
	node.TokenBalanceHex = "0x" + big.NewInt(20_000_000).Text(16) // 20 USDC on base
	adapter := evmchain.NewEVMLive(evmchain.EVMConfig{
		ChainIDStr:    gasPlanChain,
		NativeSymbol:  gasPlanNative,
		NativeDecimal: 18,
		NetworkID:     80002,
		RPCURL:        node.URL(),
	})
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(gasPlanToken)

	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: gasPlanBase}
	wallet := &models.Wallet{ID: walletID, Chain: gasPlanChain, DepositAddress: &base, MPCCurve: "secp256k1"}
	return &evmGasFixture{node: node, adapter: adapter, registry: registry, wallet: wallet, base: base}
}

func (f *evmGasFixture) plannerService() *service {
	return &service{
		registry:    f.registry,
		walletRepo:  &fakeWalletRepo{wallet: f.wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{f.base}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity(gasPlanChain)},
	}
}

func TestPlan_EVMTokenEstimatedGasMatchesBuiltTransaction(t *testing.T) {
	fixture := newEVMGasFixture(t)
	svc := fixture.plannerService()
	amount := big.NewInt(3_000_000)

	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanAsset, amount, gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatalf("PlanForWithdrawal: %v", err)
	}
	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("expected direct_from_base, got %s", plan.Strategy)
	}

	unsigned, err := fixture.adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: gasPlanBase, To: gasPlanDestination, Amount: amount, Asset: gasPlanAsset, Token: &gasPlanToken,
	})
	if err != nil {
		t.Fatalf("BuildTransfer: %v", err)
	}
	price, _ := new(big.Int).SetString(unsigned.Metadata["gas_price"].(string), 10)
	built := new(big.Int).Mul(price, new(big.Int).SetUint64(unsigned.Metadata["gas_limit"].(uint64)))

	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(built) != 0 {
		t.Fatalf("plan budgets %v wei but the built tx can spend %s wei", plan.EstimatedGas, built)
	}
}

func TestPlan_EVMTokenEstimateErrorFailsThePlan(t *testing.T) {
	fixture := newEVMGasFixture(t)
	fixture.node.EstimateGasError = "execution reverted"
	svc := fixture.plannerService()

	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanAsset, big.NewInt(3_000_000), gasPlanDestination, uuid.Nil)
	if !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("expected ErrGasEstimateFailed, got plan=%+v err=%v", plan, err)
	}
	if sent := fixture.node.CallsTo("eth_sendRawTransaction"); len(sent) != 0 {
		t.Fatalf("planning must never broadcast, got %d sends", len(sent))
	}
}

func TestPlan_EVMTokenPreviewWithoutDestinationReportsUnknownGas(t *testing.T) {
	fixture := newEVMGasFixture(t)
	svc := fixture.plannerService()

	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanAsset, big.NewInt(3_000_000), "", uuid.Nil)
	if err != nil {
		t.Fatalf("PlanForWithdrawal: %v", err)
	}
	if plan.EstimatedGas != nil {
		t.Fatalf("without a destination the token gas is unknown; got %s", plan.EstimatedGas)
	}
	if calls := fixture.node.CallsTo("eth_estimateGas"); len(calls) != 0 {
		t.Fatalf("no transfer can be simulated without a destination, got %d calls", len(calls))
	}
}

func TestPlan_MultiSweepSumsEstimatedLegLimits(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	childB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	gasPrice := big.NewInt(10_000_000_000)
	base := balanceMapChain("eth", "eth", map[string]*big.Int{
		"BASE": big.NewInt(0), "CHILD_A": big.NewInt(300), "CHILD_B": big.NewInt(250),
	})
	base.EstimateGasPriceVal = gasPrice
	perLeg := map[string]uint64{"CHILD_A": 70_000, "CHILD_B": 80_000, "BASE": 90_000}
	estimating := &estimatingMockChain{MockChain: base, estimate: func(req types.TransferRequest) (uint64, error) {
		return perLeg[req.From], nil
	}}

	registry := chain.NewRegistry()
	registry.RegisterChain(estimating)
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr, childA, childB}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity("eth")},
	}

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "eth", big.NewInt(500), "DEST", uuid.Nil)
	if err != nil {
		t.Fatalf("PlanForWithdrawal: %v", err)
	}
	if plan.Strategy != StrategyMultiSweep {
		t.Fatalf("expected multi_sweep, got %s", plan.Strategy)
	}
	// 2 legs × 21k gas_seed + 70k + 80k sweeps + 90k final.
	want := new(big.Int).Mul(gasPrice, big.NewInt(2*21_000+70_000+80_000+90_000))
	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(want) != 0 {
		t.Fatalf("EstimatedGas = %v, want %s", plan.EstimatedGas, want)
	}
	final := estimating.requests[len(estimating.requests)-1]
	if final.From != "BASE" || final.To != "DEST" || final.Amount.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("final leg must be estimated as BASE → DEST for the full amount, got %+v", final)
	}
	for _, leg := range estimating.requests[:len(estimating.requests)-1] {
		if leg.To != "BASE" {
			t.Fatalf("sweep legs must be estimated into the base address, got %+v", leg)
		}
	}
}

func TestExecute_TokenEstimateErrorDoesNotBroadcast(t *testing.T) {
	fixture := newEVMGasFixture(t)
	fixture.node.EstimateGasError = "execution reverted"
	txRepo := &fakeTxRepo{}
	svc := &service{
		registry:   fixture.registry,
		mpc:        mocks.NewMockMPCService(),
		walletRepo: &fakeWalletRepo{wallet: fixture.wallet},
		txRepo:     txRepo,
		fetchShareBFn: func(ctx context.Context, w *models.Wallet) ([]byte, error) {
			return []byte("fake-share-b"), nil
		},
	}
	source := fixture.base
	plan := &Plan{
		WalletID:      fixture.wallet.ID,
		Chain:         gasPlanChain,
		Asset:         gasPlanAsset,
		Amount:        big.NewInt(3_000_000),
		Strategy:      StrategyDirectFromBase,
		SourceAddress: &source,
	}

	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")}, uuid.New(), gasPlanDestination, "user-1")
	if !errors.Is(err, chain.ErrGasEstimateFailed) {
		t.Fatalf("expected ErrGasEstimateFailed, got %v", err)
	}
	if sent := fixture.node.CallsTo("eth_sendRawTransaction"); len(sent) != 0 {
		t.Fatalf("expected no broadcast, got %d", len(sent))
	}
	if signs := svc.mpc.(*mocks.MockMPCService).SignCalls; signs != 0 {
		t.Fatalf("expected no MPC signing after a failed estimate, got %d", signs)
	}
	if len(txRepo.created) != 0 {
		t.Fatalf("expected no persisted transaction, got %d", len(txRepo.created))
	}
}
