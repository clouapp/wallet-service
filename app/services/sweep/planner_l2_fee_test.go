package sweep

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	l2FeeTestAmount = int64(1_000_000_000_000_000) // 0.001 ETH
	// FakeEVMNode: 1 gwei suggested → 2 gwei encoded; native transfers use 21000 gas.
	l2FeeTestNativeGas = int64(2_000_000_000 * 21_000)
	// GasPriceOracle quote 10 gwei-wei, doubled by the adapter's L1 fee buffer.
	l2FeeTestL1FeeHex      = "0x2540be400"
	l2FeeTestBufferedL1Fee = int64(2 * 10_000_000_000)
)

func baseSepoliaPlanner(t *testing.T, baseBalance *big.Int) (*service, uuid.UUID) {
	t.Helper()
	node := mocks.NewFakeEVMNode(t)
	node.L1FeeHex = l2FeeTestL1FeeHex
	node.NativeBalanceHex = "0x" + baseBalance.Text(16)
	adapter := chain.NewEVMLive(chain.EVMConfig{
		ChainIDStr: models.ChainBase, NativeSymbol: models.NativeETH, NativeDecimal: 18,
		NetworkID: models.EVMNetworkIDBaseSepolia, RPCURL: node.URL(),
	})
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)

	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: gasPlanBase}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainBase, DepositAddress: &base, MPCCurve: "secp256k1"}
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{base}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity(models.ChainBase)},
	}, walletID
}

func TestPlanBase_BaseThatCoversOnlyL2GasCannotPayTheL1DataFee(t *testing.T) {
	balance := big.NewInt(l2FeeTestAmount + l2FeeTestNativeGas)
	svc, walletID := baseSepoliaPlanner(t, balance)

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeETH, big.NewInt(l2FeeTestAmount), gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s: a base without room for the L1 data fee must not withdraw directly", plan.Strategy)
	}
}

func TestPlanBase_DirectWithdrawalBudgetsGasPlusL1DataFee(t *testing.T) {
	balance := big.NewInt(l2FeeTestAmount + l2FeeTestNativeGas + l2FeeTestBufferedL1Fee)
	svc, walletID := baseSepoliaPlanner(t, balance)

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeETH, big.NewInt(l2FeeTestAmount), gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("strategy %s, want direct_from_base", plan.Strategy)
	}
	want := big.NewInt(l2FeeTestNativeGas + l2FeeTestBufferedL1Fee)
	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(want) != 0 {
		t.Fatalf("EstimatedGas %v, want gas %d + L1 fee %d", plan.EstimatedGas, l2FeeTestNativeGas, l2FeeTestBufferedL1Fee)
	}
}

// l1FeeMockChain charges a fixed L1 fee per transfer and needs more gas for a
// gas_seed than the fixed 21000, like a rollup.
type l1FeeMockChain struct {
	*estimatingMockChain
	l1Fee        *big.Int
	seedGasLimit uint64
	l1Requests   []types.TransferRequest
}

func (m *l1FeeMockChain) EstimateL1DataFee(_ context.Context, req types.TransferRequest) (*big.Int, error) {
	m.l1Requests = append(m.l1Requests, req)
	return new(big.Int).Set(m.l1Fee), nil
}

func (m *l1FeeMockChain) NativeTransferGasLimit(context.Context, string, string) (uint64, error) {
	return m.seedGasLimit, nil
}

func TestPlan_MultiSweepAddsAnL1FeePerTransferAndTheRollupSeedLimit(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	childB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainArbitrum, DepositAddress: &baseAddr}

	gasPrice := big.NewInt(10_000_000)
	balances := balanceMapChain(models.ChainArbitrum, models.NativeETH, map[string]*big.Int{
		"BASE": big.NewInt(0), "CHILD_A": big.NewInt(300), "CHILD_B": big.NewInt(250),
	})
	balances.EstimateGasPriceVal = gasPrice
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
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr, childA, childB}},
		chainRepo:   &fakeChainRepo{chain: evmChainEntity(models.ChainArbitrum)},
	}

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeETH, big.NewInt(500), "DEST", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyMultiSweep {
		t.Fatalf("expected multi_sweep, got %s", plan.Strategy)
	}
	// 2 × 32k gas_seed + 70k + 80k sweeps + 90k final, plus 5 transfers × L1 fee.
	want := new(big.Int).Mul(gasPrice, big.NewInt(2*32_000+70_000+80_000+90_000))
	want.Add(want, big.NewInt(5*1_000))
	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(want) != 0 {
		t.Fatalf("EstimatedGas = %v, want %s", plan.EstimatedGas, want)
	}
	if len(rollup.l1Requests) != 5 {
		t.Fatalf("want an L1 fee for 2 seeds + 2 sweeps + 1 withdrawal, got %d", len(rollup.l1Requests))
	}
	if seed := rollup.l1Requests[0]; seed.From != "BASE" || seed.Token != nil {
		t.Fatalf("the first transfer is a native gas_seed from base, got %+v", seed)
	}
	if final := rollup.l1Requests[4]; final.From != "BASE" || final.To != "DEST" {
		t.Fatalf("the last transfer is the withdrawal BASE → DEST, got %+v", final)
	}
}
