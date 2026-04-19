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

// ---------------------------------------------------------------------------
// fakeTxRepo — captures every Transaction.Create so tests can assert on row
// shape (origin, parent_transaction_id, from/to, etc.) without a real DB.
// All unused methods return zero values.
// ---------------------------------------------------------------------------

type fakeTxRepo struct {
	created []*models.Transaction
	createErr error
}

func (f *fakeTxRepo) Create(tx *models.Transaction) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, tx)
	return nil
}
func (f *fakeTxRepo) FindByID(id uuid.UUID) (*models.Transaction, error) { return nil, nil }
func (f *fakeTxRepo) FindByIDAndWallet(txID string, walletID uuid.UUID) (*models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) FindByIdempotencyKey(key string) (*models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) FindByWallet(walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
func (f *fakeTxRepo) FindByChainAndTxHash(chainID, txHash string) (*models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) CountByChainAndTxHash(chainID, txHash, txType string) (int64, error) {
	return 0, nil
}
func (f *fakeTxRepo) CountByChainTxHashAndLogIndex(chainID, txHash string, logIndex int, txType string) (int64, error) {
	return 0, nil
}
func (f *fakeTxRepo) FindPendingByChain(chainID string) ([]models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error { return nil }
func (f *fakeTxRepo) List(chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
func (f *fakeTxRepo) ListByWalletAndChain(walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newExecutorService wires a service instance with in-memory fakes. Tests
// override fields (txRepo, registry mock, fetchShareBFn) as needed via the
// returned struct.
func newExecutorService(
	t *testing.T,
	wallet *models.Wallet,
	mockChain *mocks.MockChain,
) (*service, *fakeTxRepo) {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)
	txRepo := &fakeTxRepo{}
	svc := &service{
		registry:   registry,
		mpc:        mocks.NewMockMPCService(),
		walletRepo: &fakeWalletRepo{wallet: wallet},
		txRepo:     txRepo,
		// webhookSvc intentionally nil: executor guards with a nil check, so
		// no webhooks are emitted but no panic occurs either.
		fetchShareBFn: func(ctx context.Context, w *models.Wallet) ([]byte, error) {
			return []byte("fake-share-b"), nil
		},
	}
	return svc, txRepo
}

// sweepMockChain returns a MockChain configured for native EVM-style sweeps:
// BuildSweep emits two unsigneds (gas_seed + sweep) when a token is present,
// or one (sweep) for native.
func sweepMockChain(id, nativeAsset string) *mocks.MockChain {
	m := mocks.NewMockChain(id)
	m.NativeAssetVal = nativeAsset
	// GetBalance is called by broadcastSweepLeg to decide gas_seed sizing.
	m.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Address: addr, Asset: nativeAsset, Amount: big.NewInt(0)}, nil
	}
	m.BuildSweepFn = func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		if req.Token != nil {
			return []types.UnsignedTx{
				{ChainID: id, RawBytes: []byte("gas_seed")},
				{ChainID: id, RawBytes: []byte("sweep")},
			}, nil
		}
		return []types.UnsignedTx{{ChainID: id, RawBytes: []byte("native_sweep")}}, nil
	}
	m.BuildTransferFn = func(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
		return &types.UnsignedTx{ChainID: id, RawBytes: []byte("transfer")}, nil
	}
	m.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		return "0xhash_" + string(signed.RawBytes[:1]), nil
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestExecute_DirectFromBase_SingleTx verifies that a direct_from_base plan
// produces exactly one persisted transaction (the withdrawal) with the
// expected origin, address routing, and ID.
func TestExecute_DirectFromBase_SingleTx(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		Address:        "0xBASE",
		ExternalUserID: "user-1",
	}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
	}

	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)

	baseCopy := baseAddr
	plan := &Plan{
		WalletID:      walletID,
		Chain:         "eth",
		Asset:         "eth",
		Amount:        big.NewInt(1000),
		Strategy:      StrategyDirectFromBase,
		SourceAddress: &baseCopy,
	}
	withdrawalTxID := uuid.New()

	res, err := svc.ExecutePlan(
		context.Background(), plan, []byte("fake-share-a"),
		withdrawalTxID, "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.FinalWithdrawTx == nil {
		t.Fatalf("expected FinalWithdrawTx, got %+v", res)
	}
	if len(res.Sweeps) != 0 {
		t.Fatalf("expected zero completed sweeps, got %d", len(res.Sweeps))
	}
	if res.FailedStep != nil {
		t.Fatalf("expected nil FailedStep, got %+v", res.FailedStep)
	}
	if len(txRepo.created) != 1 {
		t.Fatalf("expected 1 persisted tx, got %d", len(txRepo.created))
	}
	tx := txRepo.created[0]
	if tx.Origin != models.TxOriginUserRequest {
		t.Fatalf("expected origin=%s, got %q", models.TxOriginUserRequest, tx.Origin)
	}
	if tx.TxType != models.TxTypeWithdrawal {
		t.Fatalf("expected tx_type=%s, got %q", models.TxTypeWithdrawal, tx.TxType)
	}
	if tx.ID != withdrawalTxID {
		t.Fatalf("expected withdrawal tx ID %s, got %s", withdrawalTxID, tx.ID)
	}
	if tx.FromAddress != "0xBASE" || tx.ToAddress != "0xDEST" {
		t.Fatalf("expected BASE → DEST, got %s → %s", tx.FromAddress, tx.ToAddress)
	}
	if tx.ExternalUserID != "user-1" {
		t.Fatalf("expected external_user_id=user-1, got %q", tx.ExternalUserID)
	}
	if tx.ParentTransactionID != nil {
		t.Fatalf("expected no parent_transaction_id on withdrawal, got %v", tx.ParentTransactionID)
	}
}

// TestExecute_MultiSweep_LinksParent covers the full multi_sweep fan-out:
// for every sweep leg with a token, broadcastSweepLeg emits a gas_seed +
// sweep pair linked to the parent withdrawal id. The final withdrawal is
// then broadcast from the base deposit address.
//
// With 2 legs we expect 2*2 + 1 = 5 persisted rows:
//   gas_seed(child_a), sweep(child_a), gas_seed(child_b), sweep(child_b), withdrawal(base).
func TestExecute_MultiSweep_LinksParent(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		Address:        "0xBASE",
		ExternalUserID: "user-1",
	}
	childA := models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		Address:        "0xCHILD_A",
		ExternalUserID: "user-1",
	}
	childB := models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		Address:        "0xCHILD_B",
		ExternalUserID: "user-1",
	}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
	}

	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	// Register a token so `plan.Asset != native` path resolves.
	svc.registry.RegisterToken(types.Token{
		Symbol: "usdt", Name: "Tether", Contract: "0xTETHER", Decimals: 6, ChainID: "eth",
	})

	plan := &Plan{
		WalletID: walletID,
		Chain:    "eth",
		Asset:    "usdt",
		Amount:   big.NewInt(1000),
		Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{
			{From: childA, Amount: big.NewInt(600), NeedsGas: true},
			{From: childB, Amount: big.NewInt(400), NeedsGas: true},
		},
	}
	withdrawalTxID := uuid.New()

	res, err := svc.ExecutePlan(
		context.Background(), plan, []byte("fake-share-a"),
		withdrawalTxID, "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.FinalWithdrawTx == nil {
		t.Fatalf("expected FinalWithdrawTx, got %+v", res)
	}
	if res.FailedStep != nil {
		t.Fatalf("expected nil FailedStep, got %+v", res.FailedStep)
	}
	if len(res.Sweeps) != 2 {
		t.Fatalf("expected 2 completed sweeps, got %d", len(res.Sweeps))
	}
	if len(txRepo.created) != 5 {
		t.Fatalf("expected 5 persisted txs (2 gas_seed + 2 sweep + 1 withdrawal), got %d", len(txRepo.created))
	}

	// Expected order: gas_seed_a, sweep_a, gas_seed_b, sweep_b, withdrawal.
	expectOrigin := []string{
		models.TxOriginGasSeed,
		models.TxOriginSweep,
		models.TxOriginGasSeed,
		models.TxOriginSweep,
		models.TxOriginUserRequest,
	}
	expectType := []string{
		models.TxTypeGasSeed,
		models.TxTypeSweep,
		models.TxTypeGasSeed,
		models.TxTypeSweep,
		models.TxTypeWithdrawal,
	}
	// Every sweep / gas_seed row links to the withdrawal id; the final
	// withdrawal row IS the parent so it has no parent of its own.
	for i, tx := range txRepo.created {
		if tx.Origin != expectOrigin[i] {
			t.Fatalf("row %d: expected origin=%s, got %q", i, expectOrigin[i], tx.Origin)
		}
		if tx.TxType != expectType[i] {
			t.Fatalf("row %d: expected tx_type=%s, got %q", i, expectType[i], tx.TxType)
		}
		if tx.Origin == models.TxOriginUserRequest {
			continue
		}
		if tx.ParentTransactionID == nil || *tx.ParentTransactionID != withdrawalTxID {
			t.Fatalf("row %d (%s): expected parent_transaction_id=%s, got %v",
				i, tx.Origin, withdrawalTxID, tx.ParentTransactionID)
		}
	}

	// Gas_seed routing: base → child.
	gasSeedA := txRepo.created[0]
	if gasSeedA.FromAddress != "0xBASE" || gasSeedA.ToAddress != "0xCHILD_A" {
		t.Fatalf("gas_seed_a: expected BASE→CHILD_A, got %s→%s", gasSeedA.FromAddress, gasSeedA.ToAddress)
	}
	// Sweep routing: child → base, token contract carried over.
	sweepA := txRepo.created[1]
	if sweepA.FromAddress != "0xCHILD_A" || sweepA.ToAddress != "0xBASE" {
		t.Fatalf("sweep_a: expected CHILD_A→BASE, got %s→%s", sweepA.FromAddress, sweepA.ToAddress)
	}
	if sweepA.TokenContract != "0xTETHER" {
		t.Fatalf("sweep_a: expected token contract 0xTETHER, got %q", sweepA.TokenContract)
	}

	// Withdrawal: base → destination, not linked to itself.
	final := txRepo.created[4]
	if final.FromAddress != "0xBASE" || final.ToAddress != "0xDEST" {
		t.Fatalf("final: expected BASE→DEST, got %s→%s", final.FromAddress, final.ToAddress)
	}
	if final.ID != withdrawalTxID {
		t.Fatalf("final: expected ID=%s, got %s", withdrawalTxID, final.ID)
	}
	if final.ParentTransactionID != nil {
		t.Fatalf("final withdrawal should have no parent, got %v", final.ParentTransactionID)
	}
}

// TestExecute_InsufficientStrategy_ReturnsErr ensures the executor short-
// circuits plans that the planner already flagged as unreachable.
func TestExecute_InsufficientStrategy_ReturnsErr(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr, MPCCurve: "secp256k1"}
	mockChain := sweepMockChain("eth", "eth")

	svc, txRepo := newExecutorService(t, wallet, mockChain)

	plan := &Plan{
		WalletID: walletID,
		Chain:    "eth",
		Asset:    "eth",
		Amount:   big.NewInt(1000),
		Strategy: StrategyInsufficient,
	}

	res, err := svc.ExecutePlan(
		context.Background(), plan, []byte("fake-share-a"),
		uuid.New(), "0xDEST", "user-1",
	)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on insufficient plan, got %+v", res)
	}
	if len(txRepo.created) != 0 {
		t.Fatalf("expected no persisted txs on insufficient plan, got %d", len(txRepo.created))
	}
}
