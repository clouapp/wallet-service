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

const (
	testSolFee        = 5_000
	testSolRentExempt = 890_880
	testSolAmount     = 20_000_000
)

type reservingChain struct {
	*mocks.MockChain
	fee, minimumRemaining *big.Int
	err                   error
}

func (c *reservingChain) NativeTransferReserve(context.Context) (*big.Int, *big.Int, error) {
	return c.fee, c.minimumRemaining, c.err
}

func solanaReservePlanner(t *testing.T, balances map[string]int64, children []models.Address, reserveErr error) (*service, uuid.UUID) {
	t.Helper()
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "SolBase"}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainSOL, DepositAddress: &base}

	mockChain := mocks.NewMockChain(models.ChainSOL)
	mockChain.NativeAssetVal = models.NativeSOL
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Address: addr, Asset: models.NativeSOL, Amount: big.NewInt(balances[addr])}, nil
	}
	adapter := &reservingChain{MockChain: mockChain, fee: big.NewInt(testSolFee), minimumRemaining: big.NewInt(testSolRentExempt), err: reserveErr}

	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: children},
		chainRepo:   &fakeChainRepo{chain: &models.Chain{ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana}},
	}, walletID
}

func solanaChild(walletID uuid.UUID, address string) models.Address {
	return models.Address{ID: uuid.New(), WalletID: walletID, Address: address}
}

func planSolana(t *testing.T, svc *service, walletID uuid.UUID) *Plan {
	t.Helper()
	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeSOL, big.NewInt(testSolAmount), "Dest", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlan_Solana_BaseWithExactAmountCannotPayFeeAndRent(t *testing.T) {
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": testSolAmount}, nil, nil)
	if plan := planSolana(t, svc, walletID); plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s, want insufficient", plan.Strategy)
	}
}

func TestPlan_Solana_BaseJustCoveringReserveIsDirect(t *testing.T) {
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": testSolAmount + testSolFee + testSolRentExempt}, nil, nil)
	plan := planSolana(t, svc, walletID)
	if plan.Strategy != StrategyDirectFromBase || plan.Amount.Int64() != testSolAmount {
		t.Fatalf("strategy %s amount %s", plan.Strategy, plan.Amount)
	}
}

func TestPlan_Solana_ChildNeedsReserveForDirectWithdrawal(t *testing.T) {
	svc, walletID := solanaReservePlanner(t, map[string]int64{
		"ChildShort": testSolAmount + testSolFee,
		"ChildOK":    testSolAmount + testSolFee + testSolRentExempt,
	}, nil, nil)
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{solanaChild(walletID, "ChildShort"), solanaChild(walletID, "ChildOK")}}

	plan := planSolana(t, svc, walletID)
	if plan.Strategy != StrategyDirectFromChild || plan.SourceAddress == nil || plan.SourceAddress.Address != "ChildOK" {
		t.Fatalf("plan %+v", plan)
	}
}

func TestPlan_Solana_MultiSweepLegsLeaveTheFeeAndCountNetAmounts(t *testing.T) {
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": 10_000_000, "ChildA": 15_000_000}, nil, nil)
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{solanaChild(walletID, "ChildA")}}

	plan := planSolana(t, svc, walletID)
	if plan.Strategy != StrategyMultiSweep || len(plan.Sweeps) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if got := plan.Sweeps[0].Amount.Int64(); got != 15_000_000-testSolFee {
		t.Fatalf("sweep must leave the fee on the child, amount %d", got)
	}
}

func TestPlan_Solana_MultiSweepInsufficientOnceFeesAreCounted(t *testing.T) {
	// Gross balances reach amount+reserve, net of the sweep fee they do not.
	base := int64(testSolAmount + testSolRentExempt - 15_000_000)
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": base, "ChildA": 15_000_000 + testSolFee}, nil, nil)
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{solanaChild(walletID, "ChildA")}}

	if plan := planSolana(t, svc, walletID); plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s, want insufficient", plan.Strategy)
	}
}

func TestPlan_Solana_ChildThatCannotPayItsFeeIsNotSwept(t *testing.T) {
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": testSolAmount + testSolFee + testSolRentExempt - 1, "Dust": testSolFee}, nil, nil)
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{solanaChild(walletID, "Dust")}}

	plan := planSolana(t, svc, walletID)
	if plan.Strategy != StrategyInsufficient || len(plan.Sweeps) != 0 {
		t.Fatalf("plan %+v", plan)
	}
}

func TestPlan_Solana_ReserveErrorFailsThePlan(t *testing.T) {
	rpcDown := errors.New("rpc down")
	svc, walletID := solanaReservePlanner(t, map[string]int64{"SolBase": 1_000_000_000}, nil, rpcDown)
	_, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeSOL, big.NewInt(testSolAmount), "Dest", uuid.Nil)
	if !errors.Is(err, rpcDown) {
		t.Fatalf("err %v", err)
	}
}

func TestLoad_NativeReserve_RejectsInvalidValues(t *testing.T) {
	mockChain := mocks.NewMockChain(models.ChainSOL)
	mockChain.NativeAssetVal = models.NativeSOL
	for _, adapter := range []*reservingChain{
		{MockChain: mockChain, fee: nil, minimumRemaining: big.NewInt(1)},
		{MockChain: mockChain, fee: big.NewInt(-1), minimumRemaining: big.NewInt(1)},
		{MockChain: mockChain, fee: big.NewInt(1), minimumRemaining: big.NewInt(-1)},
	} {
		if _, err := loadNativeReserve(context.Background(), adapter, models.NativeSOL); err == nil {
			t.Fatalf("expected error for fee=%v min=%v", adapter.fee, adapter.minimumRemaining)
		}
	}
}

func TestLoad_NativeReserve_TokenAssetHasNoReserve(t *testing.T) {
	mockChain := mocks.NewMockChain(models.ChainSOL)
	mockChain.NativeAssetVal = models.NativeSOL
	adapter := &reservingChain{MockChain: mockChain, err: errors.New("must not be called")}
	reserve, err := loadNativeReserve(context.Background(), adapter, "USDC")
	if err != nil || reserve.fee.Sign() != 0 || reserve.minimumRemaining.Sign() != 0 {
		t.Fatalf("reserve %+v err %v", reserve, err)
	}
}
