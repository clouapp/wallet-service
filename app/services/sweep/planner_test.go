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

// ---------------------------------------------------------------------------
// In-memory fakes for the three repositories the planner touches.
// All unused interface methods return zero values so tests stay narrow.
// ---------------------------------------------------------------------------

type fakeWalletRepo struct {
	wallet *models.Wallet
}

func (f *fakeWalletRepo) Create(wallet *models.Wallet) error { return nil }
func (f *fakeWalletRepo) FindByID(id uuid.UUID) (*models.Wallet, error) {
	if f.wallet == nil {
		return nil, nil
	}
	if f.wallet.ID != id {
		return nil, nil
	}
	return f.wallet, nil
}
func (f *fakeWalletRepo) FindByIDAndAccount(id, accountID uuid.UUID) (*models.Wallet, error) {
	return nil, nil
}
func (f *fakeWalletRepo) FindAll() ([]models.Wallet, error) { return nil, nil }
func (f *fakeWalletRepo) PaginateAll(limit, offset int) ([]models.Wallet, int64, error) {
	return nil, 0, nil
}
func (f *fakeWalletRepo) PaginateByAccount(accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	return nil, 0, nil
}
func (f *fakeWalletRepo) UpdateField(id uuid.UUID, field string, value interface{}) error {
	return nil
}
func (f *fakeWalletRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	return nil
}
func (f *fakeWalletRepo) IncrementAddressIndex(id uuid.UUID) (int, error) { return 0, nil }

type fakeAddressRepo struct {
	children []models.Address
}

func (f *fakeAddressRepo) Create(addr *models.Address) error                          { return nil }
func (f *fakeAddressRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	return nil
}
func (f *fakeAddressRepo) CountByChainAndAddress(chainID, address string) (int64, error) {
	return 0, nil
}
func (f *fakeAddressRepo) FindByChainAndAddress(chainID, address string) (*models.Address, error) {
	return nil, nil
}
func (f *fakeAddressRepo) FindByExternalUserID(externalUserID string) ([]models.Address, error) {
	return nil, nil
}
func (f *fakeAddressRepo) FindByID(id uuid.UUID) (*models.Address, error) { return nil, nil }
func (f *fakeAddressRepo) FindByWalletID(walletID uuid.UUID) ([]models.Address, error) {
	return f.children, nil
}
func (f *fakeAddressRepo) MaxDerivationIndex(walletID uuid.UUID) (int, error) { return 0, nil }
func (f *fakeAddressRepo) PaginateByWalletID(walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	return nil, 0, nil
}
func (f *fakeAddressRepo) PluckActiveAddresses(chainID string) ([]string, error) {
	return nil, nil
}

type fakeChainRepo struct {
	chain *models.Chain
}

func (f *fakeChainRepo) FindAll() ([]models.Chain, error)                        { return nil, nil }
func (f *fakeChainRepo) FindActive() ([]models.Chain, error)                     { return nil, nil }
func (f *fakeChainRepo) FindByTestnet(isTestnet bool) ([]models.Chain, error)    { return nil, nil }
func (f *fakeChainRepo) Create(chain *models.Chain) error                        { return nil }
func (f *fakeChainRepo) FindByID(id string) (*models.Chain, error) {
	if f.chain == nil || f.chain.ID != id {
		return nil, nil
	}
	return f.chain, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// balanceMapChain returns a MockChain whose GetBalance/GetTokenBalance look up
// `balances` by address.Address. Missing keys resolve to zero.
func balanceMapChain(chainID, nativeAsset string, balances map[string]*big.Int) *mocks.MockChain {
	m := mocks.NewMockChain(chainID)
	m.NativeAssetVal = nativeAsset
	m.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		if b, ok := balances[addr]; ok {
			return &types.Balance{Address: addr, Asset: nativeAsset, Amount: new(big.Int).Set(b)}, nil
		}
		return &types.Balance{Address: addr, Asset: nativeAsset, Amount: big.NewInt(0)}, nil
	}
	// MockChain.DustThreshold returns big.NewInt(0) by default; the planner only
	// filters when the threshold is > 0, so zero means "no filtering" for us.
	return m
}

func newPlannerService(
	t *testing.T,
	wallet *models.Wallet,
	children []models.Address,
	mockChain *mocks.MockChain,
	chainEntity *models.Chain,
) *service {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: children},
		chainRepo:   &fakeChainRepo{chain: chainEntity},
	}
}

func evmChainEntity(id string) *models.Chain {
	return &models.Chain{ID: id, AdapterType: models.AdapterTypeEVM}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPlan_DirectFromBase(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	mockChain := balanceMapChain("eth", "usdt", map[string]*big.Int{
		"BASE": big.NewInt(1000),
	})
	svc := newPlannerService(t, wallet, []models.Address{baseAddr}, mockChain, evmChainEntity("eth"))

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "usdt", big.NewInt(500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("expected %s, got %s", StrategyDirectFromBase, plan.Strategy)
	}
	if !plan.ReachesTarget {
		t.Fatal("expected reaches_target=true")
	}
	if plan.SourceAddress == nil || plan.SourceAddress.Address != "BASE" {
		t.Fatalf("expected source BASE, got %+v", plan.SourceAddress)
	}
	if plan.BaseBalance == nil || plan.BaseBalance.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("expected BaseBalance=1000, got %v", plan.BaseBalance)
	}
}

func TestPlan_DirectFromChild_SingleCovers(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	childB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	mockChain := balanceMapChain("eth", "usdt", map[string]*big.Int{
		"BASE":    big.NewInt(0),
		"CHILD_A": big.NewInt(600),
		"CHILD_B": big.NewInt(200),
	})
	svc := newPlannerService(t, wallet, []models.Address{baseAddr, childA, childB}, mockChain, evmChainEntity("eth"))

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "usdt", big.NewInt(500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Strategy != StrategyDirectFromChild {
		t.Fatalf("expected %s, got %s", StrategyDirectFromChild, plan.Strategy)
	}
	if plan.SourceAddress == nil || plan.SourceAddress.Address != "CHILD_A" {
		t.Fatalf("expected CHILD_A, got %+v", plan.SourceAddress)
	}
	if !plan.ReachesTarget {
		t.Fatal("expected reaches_target=true")
	}
}

func TestPlan_MultiSweepGreedy(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	cA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	cB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	cC := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_C"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	mockChain := balanceMapChain("eth", "usdt", map[string]*big.Int{
		"BASE":    big.NewInt(100),
		"CHILD_A": big.NewInt(300),
		"CHILD_B": big.NewInt(250),
		"CHILD_C": big.NewInt(200),
	})
	svc := newPlannerService(t, wallet, []models.Address{baseAddr, cA, cB, cC}, mockChain, evmChainEntity("eth"))

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "usdt", big.NewInt(600))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Strategy != StrategyMultiSweep {
		t.Fatalf("expected %s, got %s", StrategyMultiSweep, plan.Strategy)
	}
	// Greedy: base=100, need 500. Take A (300)→400, need 200. Take B (250)→650 ≥ 600, stop.
	if len(plan.Sweeps) != 2 {
		t.Fatalf("expected 2 sweeps, got %d: %+v", len(plan.Sweeps), plan.Sweeps)
	}
	if plan.Sweeps[0].From.Address != "CHILD_A" {
		t.Fatalf("expected first sweep from CHILD_A, got %s", plan.Sweeps[0].From.Address)
	}
	if plan.Sweeps[1].From.Address != "CHILD_B" {
		t.Fatalf("expected second sweep from CHILD_B, got %s", plan.Sweeps[1].From.Address)
	}
	if plan.Sweeps[0].Amount.Cmp(big.NewInt(300)) != 0 {
		t.Fatalf("expected sweep[0].Amount=300 (full child balance), got %v", plan.Sweeps[0].Amount)
	}
	if !plan.Sweeps[0].NeedsGas {
		t.Fatal("expected EVM sweep to set NeedsGas=true")
	}
	if !plan.ReachesTarget {
		t.Fatal("expected reaches_target=true")
	}
}

func TestPlan_Insufficient(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	cA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	mockChain := balanceMapChain("eth", "usdt", map[string]*big.Int{
		"BASE":    big.NewInt(10),
		"CHILD_A": big.NewInt(20),
	})
	svc := newPlannerService(t, wallet, []models.Address{baseAddr, cA}, mockChain, evmChainEntity("eth"))

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "usdt", big.NewInt(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.ReachesTarget {
		t.Fatal("expected reaches_target=false")
	}
	if plan.Strategy != StrategyInsufficient {
		t.Fatalf("expected %s, got %s", StrategyInsufficient, plan.Strategy)
	}
}

func TestPlan_EVMOnlyGuard(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: "sol", DepositAddress: &baseAddr}

	mockChain := mocks.NewMockChain("sol")
	svc := newPlannerService(
		t, wallet, []models.Address{baseAddr}, mockChain,
		&models.Chain{ID: "sol", AdapterType: models.AdapterTypeSolana},
	)

	_, err := svc.PlanForWithdrawal(context.Background(), walletID, "sol", big.NewInt(100))
	if err != ErrUnsupportedChain {
		t.Fatalf("expected ErrUnsupportedChain for SOL wallet, got %v", err)
	}
}

func TestPlan_DustIgnored(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	cA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_A"}
	cB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "CHILD_B"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr}

	mockChain := balanceMapChain("eth", "usdt", map[string]*big.Int{
		"BASE":    big.NewInt(0),
		"CHILD_A": big.NewInt(1000),
		"CHILD_B": big.NewInt(10),
	})
	mockChain.DustThresholdFn = func(asset string) *big.Int { return big.NewInt(50) }

	svc := newPlannerService(t, wallet, []models.Address{baseAddr, cA, cB}, mockChain, evmChainEntity("eth"))

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "usdt", big.NewInt(500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.DustIgnored) != 1 {
		t.Fatalf("expected exactly 1 dust-ignored entry, got %d: %+v", len(plan.DustIgnored), plan.DustIgnored)
	}
	if plan.DustIgnored[0].Address.Address != "CHILD_B" {
		t.Fatalf("expected CHILD_B in DustIgnored, got %+v", plan.DustIgnored[0])
	}
	if plan.DustIgnored[0].Reason != reasonBelowDustThreshold {
		t.Fatalf("expected reason=%s, got %q", reasonBelowDustThreshold, plan.DustIgnored[0].Reason)
	}
	// CHILD_A alone (1000) covers the 500 amount — should be direct_from_child.
	if plan.Strategy != StrategyDirectFromChild {
		t.Fatalf("expected %s (CHILD_A alone covers), got %s", StrategyDirectFromChild, plan.Strategy)
	}
}
