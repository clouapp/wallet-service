package sweep

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// ---------------------------------------------------------------------------
// fakeTxRepo — captures every Transaction.Create so tests can assert on row
// shape (origin, parent_transaction_id, from/to, etc.) without a real DB.
// All unused methods return zero values.
// ---------------------------------------------------------------------------

type fakeTxRepo struct {
	created       []*models.Transaction
	createErr     error
	failAt        int
	attempts      int
	withins       int
	inside        bool
	createdInside bool
}

func (f *fakeTxRepo) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("callback is required")
	}
	f.withins++
	f.inside = true
	mark := len(f.created)
	err := fn(ctx)
	f.inside = false
	if err != nil {
		f.created = f.created[:mark]
		return err
	}
	return nil
}

func (f *fakeTxRepo) Create(_ context.Context, tx *models.Transaction) error {
	f.attempts++
	if f.failAt > 0 && f.attempts == f.failAt {
		return errors.New("insert sweep row")
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, tx)
	if f.inside {
		f.createdInside = true
	}
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
func (f *fakeTxRepo) CountInternalTransfers(chainID, txHash string, walletID uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeTxRepo) FindPendingByChain(chainID string) ([]models.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) UpdateFields(id uuid.UUID, fields map[string]interface{}) error { return nil }
func (f *fakeTxRepo) ListForAccount(accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return nil, 0, nil
}
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

	assignDerivedEVMAddresses(t, wallet, &baseAddr)
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
		context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
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
	if tx.FromAddress != baseAddr.Address || tx.ToAddress != "0xDEST" {
		t.Fatalf("expected BASE → DEST, got %s → %s", tx.FromAddress, tx.ToAddress)
	}
	if tx.ExternalUserID != "user-1" {
		t.Fatalf("expected external_user_id=user-1, got %q", tx.ExternalUserID)
	}
	if tx.Direction != models.TxDirectionOutbound {
		t.Fatalf("expected direction=%s, got %q", models.TxDirectionOutbound, tx.Direction)
	}
	if tx.Source != models.TxSourceWithdrawalFlow {
		t.Fatalf("expected source=%s, got %q", models.TxSourceWithdrawalFlow, tx.Source)
	}
	if tx.ParentTransactionID != nil {
		t.Fatalf("expected no parent_transaction_id on withdrawal, got %v", tx.ParentTransactionID)
	}
	mpcMock := svc.mpc.(*mocks.MockMPCService)
	if mpcMock.SignCalls != 1 {
		t.Fatalf("expected one mpc.Sign call, got %d", mpcMock.SignCalls)
	}
	if mockChain.SignTransactionCalls != 0 {
		t.Fatalf("expected no local SignTransaction, got %d", mockChain.SignTransactionCalls)
	}
}

func TestExecute_Plan_EVMUsesMPCSign(t *testing.T) {
	TestExecute_DirectFromBase_SingleTx(t)
}

// TestExecute_MultiSweep_LinksParent covers the full multi_sweep fan-out:
// for every sweep leg with a token, broadcastSweepLeg emits a gas_seed +
// sweep pair linked to the parent withdrawal id. The final withdrawal is
// then broadcast from the base deposit address.
//
// With 2 legs we expect 2*2 + 1 = 5 persisted rows:
//
//	gas_seed(child_a), sweep(child_a), gas_seed(child_b), sweep(child_b), withdrawal(base).
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
	assignDerivedEVMAddresses(t, wallet, &baseAddr, &childA, &childB)

	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	// Register a token so `plan.Asset != native` path resolves.
	svc.registry.(*chain.Registry).RegisterToken(types.Token{
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
		context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
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
	if gasSeedA.FromAddress != baseAddr.Address || gasSeedA.ToAddress != childA.Address {
		t.Fatalf("gas_seed_a: expected BASE→CHILD_A, got %s→%s", gasSeedA.FromAddress, gasSeedA.ToAddress)
	}
	// Sweep routing: child → base, token contract carried over.
	sweepA := txRepo.created[1]
	if sweepA.FromAddress != childA.Address || sweepA.ToAddress != baseAddr.Address {
		t.Fatalf("sweep_a: expected CHILD_A→BASE, got %s→%s", sweepA.FromAddress, sweepA.ToAddress)
	}
	if sweepA.TokenContract != "0xTETHER" {
		t.Fatalf("sweep_a: expected token contract 0xTETHER, got %q", sweepA.TokenContract)
	}

	// Withdrawal: base → destination, not linked to itself.
	final := txRepo.created[4]
	if final.FromAddress != baseAddr.Address || final.ToAddress != "0xDEST" {
		t.Fatalf("final: expected BASE→DEST, got %s→%s", final.FromAddress, final.ToAddress)
	}
	if final.ID != withdrawalTxID {
		t.Fatalf("final: expected ID=%s, got %s", withdrawalTxID, final.ID)
	}
	if final.ParentTransactionID != nil {
		t.Fatalf("final withdrawal should have no parent, got %v", final.ParentTransactionID)
	}
}

// executeSingleLegSweep runs a one-leg multi_sweep of asset from a child and
// returns the persisted rows (leg txs, then the final withdrawal).
func executeSingleLegSweep(t *testing.T, mockChain *mocks.MockChain, asset string, legAmount *big.Int) []*models.Transaction {
	t.Helper()
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE", ExternalUserID: "user-1"}
	child := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xCHILD", ExternalUserID: "user-1"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr, MPCCurve: "secp256k1"}
	assignDerivedEVMAddresses(t, wallet, &baseAddr, &child)

	svc, txRepo := newExecutorService(t, wallet, mockChain)
	svc.registry.(*chain.Registry).RegisterToken(types.Token{Symbol: "usdt", Name: "Tether", Contract: "0xTETHER", Decimals: 6, ChainID: "eth"})
	plan := &Plan{
		WalletID: walletID, Chain: "eth", Asset: asset, Amount: legAmount, Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: child, Amount: legAmount, NeedsGas: asset != "eth"}},
	}
	if _, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
		uuid.New(), "0xDEST", "user-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return txRepo.created
}

// The adapter re-sizes a native sweep at build time (balance minus the fee at the
// gas price it encodes), so the row must record what the tx moves, not the plan.
func TestExecute_Native_SweepRowRecordsTheBuiltAmount(t *testing.T) {
	builtAmount := big.NewInt(3_996_861_184_300_000)
	mockChain := sweepMockChain("eth", "eth")
	mockChain.BuildSweepFn = func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		return []types.UnsignedTx{{ChainID: "eth", RawBytes: []byte("native_sweep"), TransferAmount: builtAmount}}, nil
	}

	rows := executeSingleLegSweep(t, mockChain, "eth", big.NewInt(3_996_857_484_644_000))

	if rows[0].TxType != models.TxTypeSweep || rows[0].Amount != builtAmount.String() {
		t.Fatalf("sweep row %s amount %s, want the built %s", rows[0].TxType, rows[0].Amount, builtAmount)
	}
}

func TestExecute_Sweep_RowFallsBackToThePlannedAmount(t *testing.T) {
	legAmount := big.NewInt(600)

	rows := executeSingleLegSweep(t, sweepMockChain("eth", "eth"), "eth", legAmount)

	if rows[0].Amount != legAmount.String() {
		t.Fatalf("sweep row amount %s, want the planned %s when the adapter reports none", rows[0].Amount, legAmount)
	}
}

func TestExecute_Gas_SeedRowRecordsNativeGas(t *testing.T) {
	seedAmount := big.NewInt(42_000)
	tokenAmount := big.NewInt(600)
	mockChain := sweepMockChain("eth", "eth")
	mockChain.BuildSweepFn = func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		return []types.UnsignedTx{
			{ChainID: "eth", RawBytes: []byte("gas_seed"), TransferAmount: seedAmount},
			{ChainID: "eth", RawBytes: []byte("sweep"), TransferAmount: tokenAmount},
		}, nil
	}

	rows := executeSingleLegSweep(t, mockChain, "usdt", tokenAmount)

	seed, sweepRow := rows[0], rows[1]
	if seed.TxType != models.TxTypeGasSeed || seed.Asset != "eth" || seed.TokenContract != "" || seed.Amount != seedAmount.String() {
		t.Fatalf("gas_seed row %s %s %s (contract %q), want %s eth with no token contract",
			seed.TxType, seed.Amount, seed.Asset, seed.TokenContract, seedAmount)
	}
	if sweepRow.Asset != "usdt" || sweepRow.TokenContract != "0xTETHER" || sweepRow.Amount != tokenAmount.String() {
		t.Fatalf("sweep row %s %s (contract %q), want %s usdt 0xTETHER", sweepRow.Amount, sweepRow.Asset, sweepRow.TokenContract, tokenAmount)
	}
}

type linkedLegFixture struct {
	svc    *service
	txRepo *fakeTxRepo
	chain  *mocks.MockChain
	plan   *Plan
}

func newLinkedTokenLeg(t *testing.T) linkedLegFixture {
	t.Helper()
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE", ExternalUserID: "user-1"}
	child := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xCHILD", ExternalUserID: "user-1"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr, MPCCurve: "secp256k1"}
	assignDerivedEVMAddresses(t, wallet, &baseAddr, &child)
	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	svc.registry.(*chain.Registry).RegisterToken(types.Token{
		Symbol: "usdt", Name: "Tether", Contract: "0xTETHER", Decimals: 6, ChainID: "eth",
	})
	plan := &Plan{
		WalletID: walletID, Chain: "eth", Asset: "usdt", Amount: big.NewInt(600), Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: child, Amount: big.NewInt(600), NeedsGas: true}},
	}
	return linkedLegFixture{svc: svc, txRepo: txRepo, chain: mockChain, plan: plan}
}

func runLinkedLeg(t *testing.T, fixture linkedLegFixture) *Result {
	t.Helper()
	res, err := fixture.svc.ExecutePlan(
		context.Background(), fixture.plan, SigningCredentials{ShareA: []byte("fake-share-a")},
		uuid.New(), "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func TestExecute_Linked_LegCommitsGasSeedAndSweepTogether(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	res := runLinkedLeg(t, fixture)
	if res.FailedStep != nil || res.FinalWithdrawTx == nil {
		t.Fatalf("leg failed: %+v", res)
	}
	if fixture.txRepo.withins != 2 || !fixture.txRepo.createdInside {
		t.Fatalf("withins=%d inside=%v, want the leg and the withdrawal", fixture.txRepo.withins, fixture.txRepo.createdInside)
	}
	if len(fixture.txRepo.created) != 3 {
		t.Fatalf("rows %d, want gas_seed, sweep, withdrawal", len(fixture.txRepo.created))
	}
	if fixture.txRepo.created[0].TxType != models.TxTypeGasSeed || fixture.txRepo.created[1].TxType != models.TxTypeSweep {
		t.Fatalf("order %s then %s", fixture.txRepo.created[0].TxType, fixture.txRepo.created[1].TxType)
	}
}

func TestExecute_Linked_LegRollsBackWhenTheSweepRowFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	fixture.txRepo.failAt = 2
	res := runLinkedLeg(t, fixture)
	if res == nil || res.FailedStep == nil || res.FinalWithdrawTx != nil {
		t.Fatalf("want a failed leg and no withdrawal, got %+v", res)
	}
	if len(fixture.txRepo.created) != 0 || fixture.txRepo.withins != 1 {
		t.Fatalf("created=%d withins=%d, want the gas seed rolled back with the sweep row", len(fixture.txRepo.created), fixture.txRepo.withins)
	}
}

func TestExecute_Linked_LegKeepsGasSeedWhenSweepBroadcastFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	var broadcasts int
	fixture.chain.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		broadcasts++
		if broadcasts == 2 {
			return "", errors.New("rpc: sweep broadcast failed")
		}
		return "0xhash_" + string(signed.RawBytes[:1]), nil
	}
	res := runLinkedLeg(t, fixture)
	if res == nil || res.FailedStep == nil || len(res.Sweeps) != 0 {
		t.Fatalf("want the leg failed before it counts as swept, got %+v", res)
	}
	if len(fixture.txRepo.created) != 1 || fixture.txRepo.created[0].TxType != models.TxTypeGasSeed {
		t.Fatalf("rows %+v, want only the broadcast gas seed", fixture.txRepo.created)
	}
}

type recordingSweepEvents struct {
	inside             *bool
	stagedIn           bool
	sent               int
	failStage          bool
	withdrawalStagedIn bool
	withdrawalSent     int
	failWithdrawal     bool
}

func (r *recordingSweepEvents) EnqueueEvent(context.Context, uuid.UUID, types.EventType, interface{}) {
}

func (r *recordingSweepEvents) StageSweepBroadcast(context.Context, *models.Transaction) (func(context.Context), error) {
	if r.inside != nil {
		r.stagedIn = *r.inside
	}
	if r.failStage {
		return nil, errors.New("insert webhook event")
	}
	return func(context.Context) { r.sent++ }, nil
}

func (r *recordingSweepEvents) StageWithdrawalBroadcasting(context.Context, *models.Transaction) (func(context.Context), error) {
	if r.inside != nil {
		r.withdrawalStagedIn = *r.inside
	}
	if r.failWithdrawal {
		return nil, errors.New("insert webhook event")
	}
	return func(context.Context) { r.withdrawalSent++ }, nil
}

func TestExecute_Linked_LegRollsBackWhenTheWebhookInsertFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	events := &recordingSweepEvents{inside: &fixture.txRepo.inside, failStage: true}
	fixture.svc.webhookSvc = events
	res := runLinkedLeg(t, fixture)
	if res == nil || res.FailedStep == nil {
		t.Fatalf("want the leg failed, got %+v", res)
	}
	if !events.stagedIn || events.sent != 0 || len(fixture.txRepo.created) != 0 {
		t.Fatalf("stagedIn=%v sent=%d rows=%d", events.stagedIn, events.sent, len(fixture.txRepo.created))
	}
}

func TestExecute_Linked_LegSendsTheWebhookAfterCommit(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	events := &recordingSweepEvents{inside: &fixture.txRepo.inside}
	fixture.svc.webhookSvc = events
	res := runLinkedLeg(t, fixture)
	if res == nil || res.FailedStep != nil {
		t.Fatalf("want the leg committed, got %+v", res)
	}
	if !events.stagedIn || events.sent != 1 || len(fixture.txRepo.created) != 3 {
		t.Fatalf("stagedIn=%v sent=%d rows=%d", events.stagedIn, events.sent, len(fixture.txRepo.created))
	}
}

func runManualLeg(t *testing.T, fixture linkedLegFixture) (string, error) {
	t.Helper()
	wallet := fixture.svc.walletRepo.(*fakeWalletRepo).wallet
	return fixture.svc.broadcastLeg(
		context.Background(),
		fixture.chain,
		mpcpkg.CurveSecp256k1,
		walletKeys{shareA: []byte("fake-share-a"), shareB: []byte("fake-share-b")},
		wallet,
		fixture.plan,
		fixture.plan.Sweeps[0],
		uuid.New(),
		legBroadcastOpts{Origin: models.TxOriginManualConsolidation},
	)
}

func TestConsolidate_Manual_LegCommitsGasSeedSweepAndWebhookTogether(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	events := &recordingSweepEvents{inside: &fixture.txRepo.inside}
	fixture.svc.webhookSvc = events
	hash, err := runManualLeg(t, fixture)
	if err != nil || hash == "" {
		t.Fatalf("hash %q err %v", hash, err)
	}
	if fixture.txRepo.withins != 1 || !fixture.txRepo.createdInside {
		t.Fatalf("withins=%d inside=%v", fixture.txRepo.withins, fixture.txRepo.createdInside)
	}
	if len(fixture.txRepo.created) != 2 {
		t.Fatalf("rows %d, want gas_seed and sweep", len(fixture.txRepo.created))
	}
	seed, sweepRow := fixture.txRepo.created[0], fixture.txRepo.created[1]
	if seed.TxType != models.TxTypeGasSeed || seed.Origin != models.TxOriginGasSeed || seed.ParentTransactionID != nil {
		t.Fatalf("gas seed %+v", seed)
	}
	if sweepRow.TxType != models.TxTypeSweep || sweepRow.Origin != models.TxOriginManualConsolidation || sweepRow.ParentTransactionID != nil {
		t.Fatalf("sweep %+v", sweepRow)
	}
	if !events.stagedIn || events.sent != 1 {
		t.Fatalf("stagedIn=%v sent=%d", events.stagedIn, events.sent)
	}
	if fixture.svc.walletRepo.(*fakeWalletRepo).updateCalls != 0 {
		t.Fatal("gas status must stay outside this commit")
	}
}

func TestConsolidate_Manual_LegRollsBackWhenTheSweepRowFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	fixture.txRepo.failAt = 2
	if _, err := runManualLeg(t, fixture); err == nil {
		t.Fatal("want the sweep insert to fail the leg")
	}
	if len(fixture.txRepo.created) != 0 || fixture.txRepo.withins != 1 {
		t.Fatalf("created=%d withins=%d, want the gas seed rolled back with the sweep row", len(fixture.txRepo.created), fixture.txRepo.withins)
	}
}

func TestConsolidate_Manual_LegKeepsGasSeedWhenSweepBroadcastFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	var broadcasts int
	fixture.chain.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		broadcasts++
		if broadcasts == 2 {
			return "", errors.New("rpc: sweep broadcast failed")
		}
		return "0xhash_" + string(signed.RawBytes[:1]), nil
	}
	if _, err := runManualLeg(t, fixture); err == nil {
		t.Fatal("want the sweep broadcast to fail the leg")
	}
	if len(fixture.txRepo.created) != 1 || fixture.txRepo.created[0].TxType != models.TxTypeGasSeed {
		t.Fatalf("rows %+v, want only the broadcast gas seed", fixture.txRepo.created)
	}
}

func TestConsolidate_Manual_LegRollsBackWhenTheWebhookInsertFails(t *testing.T) {
	fixture := newLinkedTokenLeg(t)
	events := &recordingSweepEvents{inside: &fixture.txRepo.inside, failStage: true}
	fixture.svc.webhookSvc = events
	if _, err := runManualLeg(t, fixture); err == nil {
		t.Fatal("want the webhook insert to fail the leg")
	}
	if !events.stagedIn || events.sent != 0 || len(fixture.txRepo.created) != 0 {
		t.Fatalf("stagedIn=%v sent=%d rows=%d", events.stagedIn, events.sent, len(fixture.txRepo.created))
	}
}

func directWithdrawalPlan(walletID uuid.UUID, base models.Address) *Plan {
	baseCopy := base
	return &Plan{
		WalletID:      walletID,
		Chain:         "eth",
		Asset:         "eth",
		Amount:        big.NewInt(1000),
		Strategy:      StrategyDirectFromBase,
		SourceAddress: &baseCopy,
	}
}

func TestExecute_Withdrawal_CommitsRowAndWebhookTogether(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE", ExternalUserID: "user-1"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr, MPCCurve: "secp256k1"}
	assignDerivedEVMAddresses(t, wallet, &baseAddr)
	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	events := &recordingSweepEvents{inside: &txRepo.inside}
	svc.webhookSvc = events

	res, err := svc.ExecutePlan(
		context.Background(), directWithdrawalPlan(walletID, baseAddr),
		SigningCredentials{ShareA: []byte("fake-share-a")}, uuid.New(), "0xDEST", "user-1",
	)
	if err != nil || res == nil || res.FinalWithdrawTx == nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	if txRepo.withins != 1 || !txRepo.createdInside || len(txRepo.created) != 1 {
		t.Fatalf("withins=%d inside=%v rows=%d", txRepo.withins, txRepo.createdInside, len(txRepo.created))
	}
	if txRepo.created[0].TxType != models.TxTypeWithdrawal {
		t.Fatalf("row %s", txRepo.created[0].TxType)
	}
	if !events.withdrawalStagedIn || events.withdrawalSent != 1 || events.sent != 0 {
		t.Fatalf("stagedIn=%v sent=%d sweepSent=%d", events.withdrawalStagedIn, events.withdrawalSent, events.sent)
	}
	if svc.walletRepo.(*fakeWalletRepo).updateCalls != 0 {
		t.Fatal("gas status must stay outside this commit")
	}
}

func TestExecute_Withdrawal_RollsBackWhenTheWebhookInsertFails(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE", ExternalUserID: "user-1"}
	wallet := &models.Wallet{ID: walletID, Chain: "eth", DepositAddress: &baseAddr, MPCCurve: "secp256k1"}
	assignDerivedEVMAddresses(t, wallet, &baseAddr)
	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	events := &recordingSweepEvents{inside: &txRepo.inside, failWithdrawal: true}
	svc.webhookSvc = events

	_, err := svc.ExecutePlan(
		context.Background(), directWithdrawalPlan(walletID, baseAddr),
		SigningCredentials{ShareA: []byte("fake-share-a")}, uuid.New(), "0xDEST", "user-1",
	)
	if err == nil {
		t.Fatal("want the webhook insert to fail the withdrawal")
	}
	if !events.withdrawalStagedIn || events.withdrawalSent != 0 || len(txRepo.created) != 0 || txRepo.withins != 1 {
		t.Fatalf("stagedIn=%v sent=%d rows=%d withins=%d", events.withdrawalStagedIn, events.withdrawalSent, len(txRepo.created), txRepo.withins)
	}
}

func TestStage_Withdrawal_BroadcastingInsertsTheRowBeforeSending(t *testing.T) {
	walletID := uuid.New()
	tx := &models.Transaction{ID: uuid.New(), WalletID: walletID, TxType: models.TxTypeWithdrawal}
	events := &fakeWebhookEventRepo{}
	var sentBeforeInsert bool
	sender := &orderQueueSender{events: events, sentBeforeInsert: &sentBeforeInsert}
	svc := webhook.NewService(webhook.Deps{
		SQS: sender,
		Configs: &fakeWebhookConfigRepo{configs: []models.WebhookConfig{{
			ID: uuid.New(), URL: "https://example.test/hooks", Secret: "s",
			Events: `{"withdrawal.broadcasting"}`, IsActive: true,
		}}},
		Events: events,
	})
	send, err := svc.StageWithdrawalBroadcasting(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.created) != 1 || sender.sent != 0 {
		t.Fatalf("created=%d sent=%d before the closure", len(events.created), sender.sent)
	}
	send(context.Background())
	if sentBeforeInsert || sender.sent != 1 || events.created[0].EventType != string(types.EventWithdrawalBroadcasting) {
		t.Fatalf("sentBeforeInsert=%v sent=%d type=%s", sentBeforeInsert, sender.sent, events.created[0].EventType)
	}
}

func TestStage_Sweep_BroadcastInsertsTheRowBeforeSending(t *testing.T) {
	walletID := uuid.New()
	tx := &models.Transaction{ID: uuid.New(), WalletID: walletID, TxType: models.TxTypeSweep}
	events := &fakeWebhookEventRepo{}
	var sentBeforeInsert bool
	sender := &orderQueueSender{events: events, sentBeforeInsert: &sentBeforeInsert}
	svc := webhook.NewService(webhook.Deps{
		SQS: sender,
		Configs: &fakeWebhookConfigRepo{configs: []models.WebhookConfig{{
			ID: uuid.New(), URL: "https://example.test/hooks", Secret: "s",
			Events: `{"sweep.broadcast"}`, IsActive: true,
		}}},
		Events: events,
	})
	send, err := svc.StageSweepBroadcast(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.created) != 1 || sender.sent != 0 {
		t.Fatalf("created=%d sent=%d before the closure", len(events.created), sender.sent)
	}
	send(context.Background())
	if sentBeforeInsert || sender.sent != 1 || events.created[0].EventType != string(types.EventSweepBroadcast) {
		t.Fatalf("sentBeforeInsert=%v sent=%d type=%s", sentBeforeInsert, sender.sent, events.created[0].EventType)
	}
}

func TestStage_Sweep_ConfirmedInsertsTheRowBeforeSending(t *testing.T) {
	walletID := uuid.New()
	tx := &models.Transaction{ID: uuid.New(), WalletID: walletID, TxType: models.TxTypeSweep}
	events := &fakeWebhookEventRepo{}
	var sentBeforeInsert bool
	sender := &orderQueueSender{events: events, sentBeforeInsert: &sentBeforeInsert}
	svc := webhook.NewService(webhook.Deps{
		SQS: sender,
		Configs: &fakeWebhookConfigRepo{configs: []models.WebhookConfig{{
			ID: uuid.New(), URL: "https://example.test/hooks", Secret: "s",
			Events: `{"sweep.confirmed"}`, IsActive: true,
		}}},
		Events: events,
	})
	send, err := svc.StageSweepConfirmed(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.created) != 1 || sender.sent != 0 {
		t.Fatalf("created=%d sent=%d before the closure", len(events.created), sender.sent)
	}
	if events.created[0].TransactionID == nil || *events.created[0].TransactionID != tx.ID || events.created[0].EventType != string(types.EventSweepConfirmed) {
		t.Fatalf("transaction=%v type=%s", events.created[0].TransactionID, events.created[0].EventType)
	}
	send(context.Background())
	if sentBeforeInsert || sender.sent != 1 {
		t.Fatalf("sentBeforeInsert=%v sent=%d", sentBeforeInsert, sender.sent)
	}
}

type orderQueueSender struct {
	events           *fakeWebhookEventRepo
	sent             int
	sentBeforeInsert *bool
}

func (o *orderQueueSender) SendWebhook(context.Context, types.WebhookMessage) error {
	if len(o.events.created) == 0 && o.sentBeforeInsert != nil {
		*o.sentBeforeInsert = true
	}
	o.sent++
	return nil
}

// TestExecute_MultiSweep_RetryAfterPartialFailure simulates the end-to-end
// retry flow for a multi-leg sweep: the first attempt broadcasts leg 0
// successfully (gas_seed + sweep), fails mid-way through leg 1 (the gas_seed
// broadcast returns an error), and reports the failure via FailedStep. The
// caller (withdraw service) would then re-plan from the current wallet state
// and call ExecutePlan again with a fresh plan containing only the
// still-outstanding legs. This test simulates that second attempt by feeding a
// new plan with a single leg and verifying it completes cleanly, covering the
// "retry with same idempotency key re-plans from current state" invariant.
func TestExecute_MultiSweep_RetryAfterPartialFailure(t *testing.T) {
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
	assignDerivedEVMAddresses(t, wallet, &baseAddr, &childA, &childB)

	mockChain := sweepMockChain("eth", "eth")
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	svc.registry.(*chain.Registry).RegisterToken(types.Token{
		Symbol: "usdt", Name: "Tether", Contract: "0xTETHER", Decimals: 6, ChainID: "eth",
	})

	// Fail broadcast call #3 (0-indexed #2): that's the childB gas_seed, i.e.
	// the first action of leg 1. Leg 0 (childA gas_seed + sweep) has already
	// completed; leg 1 is interrupted before any on-chain side-effect.
	const failAt = 3
	var broadcastCalls int
	mockChain.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		broadcastCalls++
		if broadcastCalls == failAt {
			return "", errors.New("rpc: simulated broadcast failure")
		}
		return "0xhash_" + string(signed.RawBytes[:1]), nil
	}

	// ---- First attempt -----------------------------------------------------
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
		context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
		withdrawalTxID, "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("first attempt: unexpected error: %v", err)
	}
	if res == nil {
		t.Fatalf("first attempt: expected non-nil result with FailedStep")
	}
	if res.FailedStep == nil {
		t.Fatalf("first attempt: expected FailedStep to be set after mid-plan failure")
	}
	if res.FailedStep.Index != 1 {
		t.Fatalf("first attempt: expected FailedStep.Index=1 (leg B), got %d", res.FailedStep.Index)
	}
	if !res.FailedStep.RetryReady {
		t.Fatalf("first attempt: expected RetryReady=true for transient broadcast error")
	}
	if !strings.Contains(res.FailedStep.LastError, "simulated broadcast failure") {
		t.Fatalf("first attempt: LastError should contain upstream error, got %q", res.FailedStep.LastError)
	}
	if res.FinalWithdrawTx != nil {
		t.Fatalf("first attempt: withdrawal must not be broadcast after sweep failure, got %+v", res.FinalWithdrawTx)
	}
	if len(res.Sweeps) != 1 {
		t.Fatalf("first attempt: expected 1 completed sweep leg, got %d", len(res.Sweeps))
	}
	if res.Sweeps[0].From.ID != childA.ID {
		t.Fatalf("first attempt: expected completed sweep from childA, got %s", res.Sweeps[0].From.Address)
	}
	// Persisted rows so far: gas_seed(A) + sweep(A) = 2. The failing childB
	// gas_seed aborts before any row is written and no withdrawal row exists.
	if len(txRepo.created) != 2 {
		t.Fatalf("first attempt: expected 2 persisted txs (childA leg only), got %d", len(txRepo.created))
	}
	if txRepo.created[0].Origin != models.TxOriginGasSeed || txRepo.created[1].Origin != models.TxOriginSweep {
		t.Fatalf("first attempt: unexpected origin sequence: %q, %q",
			txRepo.created[0].Origin, txRepo.created[1].Origin)
	}

	// ---- Second attempt: planner would have detected childA is now at zero
	// balance and returned a single-leg plan covering only childB. We feed
	// that re-planned value in directly; broadcasts now all succeed because
	// our failure trigger fires only on call #3 which has already passed.
	retryPlan := &Plan{
		WalletID: walletID,
		Chain:    "eth",
		Asset:    "usdt",
		Amount:   big.NewInt(1000),
		Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{
			{From: childB, Amount: big.NewInt(400), NeedsGas: true},
		},
	}

	retryRes, err := svc.ExecutePlan(
		context.Background(), retryPlan, SigningCredentials{ShareA: []byte("fake-share-a")},
		withdrawalTxID, "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("retry: unexpected error: %v", err)
	}
	if retryRes == nil || retryRes.FinalWithdrawTx == nil {
		t.Fatalf("retry: expected successful FinalWithdrawTx, got %+v", retryRes)
	}
	if retryRes.FailedStep != nil {
		t.Fatalf("retry: expected nil FailedStep, got %+v", retryRes.FailedStep)
	}
	if len(retryRes.Sweeps) != 1 || retryRes.Sweeps[0].From.ID != childB.ID {
		t.Fatalf("retry: expected single sweep from childB, got %+v", retryRes.Sweeps)
	}
	// Retry adds: gas_seed(B) + sweep(B) + withdrawal = 3, running total 5.
	if len(txRepo.created) != 5 {
		t.Fatalf("retry: expected 5 persisted txs cumulatively, got %d", len(txRepo.created))
	}
	final := txRepo.created[4]
	if final.TxType != models.TxTypeWithdrawal {
		t.Fatalf("retry: expected final row to be withdrawal, got %s", final.TxType)
	}
	if final.ID != withdrawalTxID {
		t.Fatalf("retry: final withdrawal ID must preserve idempotency key %s, got %s",
			withdrawalTxID, final.ID)
	}
}

// ---------------------------------------------------------------------------
// Fakes for exercising *webhook.Service without touching SQS or the DB.
// ---------------------------------------------------------------------------

type recordedWebhook struct {
	eventType types.EventType
	txID      uuid.UUID
}

type fakeQueueSender struct {
	sent []recordedWebhook
}

func (f *fakeQueueSender) SendWebhook(ctx context.Context, msg types.WebhookMessage) error {
	txID, _ := uuid.Parse(msg.TransactionID)
	f.sent = append(f.sent, recordedWebhook{eventType: msg.EventType, txID: txID})
	return nil
}

type fakeWebhookConfigRepo struct {
	configs []models.WebhookConfig
}

func (f *fakeWebhookConfigRepo) Create(_ context.Context, cfg *models.WebhookConfig) error {
	return nil
}
func (f *fakeWebhookConfigRepo) FindActive(_ context.Context) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *fakeWebhookConfigRepo) FindAll(_ context.Context) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *fakeWebhookConfigRepo) FindVisibleToAccount(_ context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *fakeWebhookConfigRepo) FindByID(_ context.Context, id uuid.UUID) (*models.WebhookConfig, error) {
	return nil, nil
}
func (f *fakeWebhookConfigRepo) AssignAccount(_ context.Context, id, accountID uuid.UUID, events *string, isActive *bool) error {
	return nil
}
func (f *fakeWebhookConfigRepo) DeleteByID(_ context.Context, id uuid.UUID) error { return nil }

type fakeWebhookEventRepo struct {
	created []*models.WebhookEvent
}

func (f *fakeWebhookEventRepo) Create(_ context.Context, event *models.WebhookEvent) error {
	f.created = append(f.created, event)
	return nil
}
func (f *fakeWebhookEventRepo) AlreadyDelivered(context.Context, string) (bool, error) {
	return false, nil
}
func (f *fakeWebhookEventRepo) MarkDelivered(_ context.Context, eventID string) error { return nil }
func (f *fakeWebhookEventRepo) IncrementAttempt(_ context.Context, eventID, errMsg string) error {
	return nil
}
func (f *fakeWebhookEventRepo) ExistsForSubject(_ context.Context, configID uuid.UUID, eventType, subjectID string) (bool, error) {
	return false, nil
}
func (f *fakeWebhookEventRepo) FindDueForDelivery(_ context.Context, limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	return nil, nil
}
func (f *fakeWebhookEventRepo) MarkFailed(_ context.Context, eventID, errMsg string) error {
	return nil
}

// TestExecute_MultiSweep_WebhookEmittedPerSweep verifies that for a multi-leg
// plan the executor emits exactly one sweep.broadcast event per leg (never per
// gas_seed) and exactly one withdrawal.broadcasting event for the final
// transfer. Each event must carry the correct transaction id so downstream
// consumers can correlate deliveries with the parent withdrawal.
func TestExecute_MultiSweep_WebhookEmittedPerSweep(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{
		ID:             uuid.New(),
		WalletID:       walletID,
		Address:        "0xBASE",
		ExternalUserID: "user-1",
	}
	childA := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xCHILD_A", ExternalUserID: "user-1"}
	childB := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xCHILD_B", ExternalUserID: "user-1"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
	}
	assignDerivedEVMAddresses(t, wallet, &baseAddr, &childA, &childB)

	mockChain := sweepMockChain("eth", "eth")
	svc, _ := newExecutorService(t, wallet, mockChain)
	svc.registry.(*chain.Registry).RegisterToken(types.Token{
		Symbol: "usdt", Name: "Tether", Contract: "0xTETHER", Decimals: 6, ChainID: "eth",
	})

	// Wire a real webhook.Service backed by fakes; one active config that
	// subscribes to both event types we care about. The Events column uses
	// a postgres-array string format (see webhook.pgArray).
	sender := &fakeQueueSender{}
	cfgRepo := &fakeWebhookConfigRepo{
		configs: []models.WebhookConfig{{
			ID:       uuid.New(),
			URL:      "https://example.test/hooks",
			Secret:   "s",
			Events:   `{"sweep.broadcast","withdrawal.broadcasting"}`,
			IsActive: true,
		}},
	}
	eventRepo := &fakeWebhookEventRepo{}
	svc.webhookSvc = webhook.NewService(webhook.Deps{
		SQS:     sender,
		Configs: cfgRepo,
		Events:  eventRepo,
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
		context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
		withdrawalTxID, "0xDEST", "user-1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.FinalWithdrawTx == nil {
		t.Fatalf("expected successful FinalWithdrawTx, got %+v", res)
	}

	var sweepEvents, withdrawalEvents, otherEvents int
	for _, ev := range sender.sent {
		switch ev.eventType {
		case types.EventSweepBroadcast:
			sweepEvents++
		case types.EventWithdrawalBroadcasting:
			withdrawalEvents++
			if ev.txID != withdrawalTxID {
				t.Fatalf("withdrawal.broadcasting event must carry withdrawalTxID %s, got %s",
					withdrawalTxID, ev.txID)
			}
		default:
			otherEvents++
		}
	}
	if sweepEvents != 2 {
		t.Fatalf("expected 2 sweep.broadcast events (one per leg, never for gas_seed), got %d", sweepEvents)
	}
	if withdrawalEvents != 1 {
		t.Fatalf("expected exactly 1 withdrawal.broadcasting event, got %d", withdrawalEvents)
	}
	if otherEvents != 0 {
		t.Fatalf("unexpected non-sweep/withdrawal events enqueued: %d", otherEvents)
	}
	// One WebhookEvent row per SQS send because we configured a single
	// subscriber. Parity guards against accidental double-bookkeeping.
	if len(eventRepo.created) != len(sender.sent) {
		t.Fatalf("webhook_events rows (%d) must mirror SQS sends (%d)",
			len(eventRepo.created), len(sender.sent))
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
		context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")},
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
