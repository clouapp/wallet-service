package sweep

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newGasReadinessService wires a sweep.service with in-memory fakes suitable
// for RefreshGasStatus tests. webhookSvc stays nil — production code nil-guards
// the call so no webhook is emitted but no panic occurs either.
func newGasReadinessService(
	t *testing.T,
	wallet *models.Wallet,
	mockChain *mocks.MockChain,
	chainEntity *models.Chain,
) (*service, *fakeWalletRepo) {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)
	walletRepo := &fakeWalletRepo{wallet: wallet}
	return &service{
		registry:   registry,
		walletRepo: walletRepo,
		chainRepo:  &fakeChainRepo{chain: chainEntity},
	}, walletRepo
}

func evmChainWithThreshold(id, threshold string) *models.Chain {
	c := &models.Chain{
		ID:           id,
		AdapterType:  models.AdapterTypeEVM,
		NativeSymbol: "eth",
	}
	c.GasReadinessThresholdRaw = &threshold
	return c
}

func gasMockChain(id string, balance *big.Int) *mocks.MockChain {
	m := mocks.NewMockChain(id)
	m.NativeAssetVal = "eth"
	m.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Address: addr, Asset: "eth", Amount: new(big.Int).Set(balance)}, nil
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRefresh_GasStatus_UnseededToSeeded(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}

	// threshold = 0.005 ETH; balance = 0.01 ETH → seeded
	mockChain := gasMockChain("eth", big.NewInt(10_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusSeeded {
		t.Fatalf("expected seeded, got %s", status.Status)
	}
	if status.BaseAddress != "0xBASE" {
		t.Fatalf("expected base_address=0xBASE, got %s", status.BaseAddress)
	}
	if status.NativeBalance == nil || status.NativeBalance.Cmp(big.NewInt(10_000_000_000_000_000)) != 0 {
		t.Fatalf("expected native_balance=0.01 ETH, got %v", status.NativeBalance)
	}
	if status.Threshold == nil || status.Threshold.Cmp(big.NewInt(5_000_000_000_000_000)) != 0 {
		t.Fatalf("expected threshold=0.005 ETH, got %v", status.Threshold)
	}

	if walletRepo.updateCalls != 1 {
		t.Fatalf("expected 1 UpdateFields call, got %d", walletRepo.updateCalls)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusSeeded {
		t.Fatalf("expected gas_status=seeded in update, got %v (full=%+v)", got, walletRepo.lastUpdates)
	}
	if _, ok := walletRepo.lastUpdates["gas_last_checked_at"].(time.Time); !ok {
		t.Fatalf("expected gas_last_checked_at to be time.Time, got %T", walletRepo.lastUpdates["gas_last_checked_at"])
	}
}

func TestRefresh_GasStatus_ZeroBalanceIsUnseeded(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusSeeded, // previously seeded → will transition down
	}

	mockChain := gasMockChain("eth", big.NewInt(0))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusUnseeded {
		t.Fatalf("expected unseeded for zero balance, got %s", status.Status)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusUnseeded {
		t.Fatalf("expected gas_status=unseeded persisted, got %v", got)
	}
}

func TestRefresh_GasStatus_BelowThresholdIsLow(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}

	// 0.001 ETH balance, 0.005 ETH threshold → "low"
	mockChain := gasMockChain("eth", big.NewInt(1_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusLow {
		t.Fatalf("expected low, got %s", status.Status)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusLow {
		t.Fatalf("expected gas_status=low persisted, got %v", got)
	}
}

func TestRefresh_GasStatus_BitcoinAlwaysSeeded(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "tb1qbase"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "btc",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}

	// If the adapter is ever called, the test will fail: BTC must short-circuit.
	mockChain := mocks.NewMockChain("btc")
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		t.Fatalf("BTC path must not call GetBalance (got addr=%s)", addr)
		return nil, errors.New("should not be called")
	}
	chainEntity := &models.Chain{
		ID:           "btc",
		AdapterType:  models.AdapterTypeBitcoin,
		NativeSymbol: "btc",
	}

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusSeeded {
		t.Fatalf("expected seeded for BTC, got %s", status.Status)
	}
	if status.NativeBalance != nil {
		t.Fatalf("expected nil native_balance for BTC (no check performed), got %v", status.NativeBalance)
	}
	if status.Threshold != nil {
		t.Fatalf("expected nil threshold for BTC, got %v", status.Threshold)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusSeeded {
		t.Fatalf("expected gas_status=seeded persisted on transition, got %v", got)
	}
}

func TestRefresh_GasStatus_NoTransitionNoStatusUpdate(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusSeeded, // already seeded
	}

	// Balance well above threshold → stays seeded (no transition).
	mockChain := gasMockChain("eth", big.NewInt(100_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusSeeded {
		t.Fatalf("expected seeded, got %s", status.Status)
	}

	// Only gas_last_checked_at should be updated — NOT gas_status.
	if walletRepo.updateCalls != 1 {
		t.Fatalf("expected 1 UpdateFields call, got %d", walletRepo.updateCalls)
	}
	if _, ok := walletRepo.lastUpdates["gas_status"]; ok {
		t.Fatalf("expected NO gas_status in updates on no-transition, got %+v", walletRepo.lastUpdates)
	}
	if _, ok := walletRepo.lastUpdates["gas_last_checked_at"]; !ok {
		t.Fatalf("expected gas_last_checked_at in updates, got %+v", walletRepo.lastUpdates)
	}
	if status.LastCheckedAt <= 0 {
		t.Fatalf("expected LastCheckedAt > 0, got %d", status.LastCheckedAt)
	}
}

func TestRefresh_GasStatus_UsesInjectedFallbackWhenRowHasNone(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}
	mockChain := gasMockChain("eth", big.NewInt(10_000_000_000_000_000))
	chainEntity := &models.Chain{
		ID:           "eth",
		AdapterType:  models.AdapterTypeEVM,
		NativeSymbol: "eth",
	}

	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)
	svc.gasDefaults = map[string]GasReadinessDefault{
		"eth": {Raw: "5000000000000000"},
	}

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusSeeded {
		t.Fatalf("expected seeded from injected fallback, got %s", status.Status)
	}
	if status.Threshold == nil || status.Threshold.Cmp(big.NewInt(5_000_000_000_000_000)) != 0 {
		t.Fatalf("expected injected threshold, got %v", status.Threshold)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusSeeded {
		t.Fatalf("expected gas_status=seeded persisted, got %v", got)
	}
}

func TestRefresh_GasStatus_RowThresholdBeatsInjectedFallback(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}
	// Balance is above the row threshold and below the injected fallback.
	mockChain := gasMockChain("eth", big.NewInt(2))
	chainEntity := evmChainWithThreshold("eth", "1")

	svc, _ := newGasReadinessService(t, wallet, mockChain, chainEntity)
	svc.gasDefaults = map[string]GasReadinessDefault{
		"eth": {Raw: "100"},
	}

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != models.GasStatusSeeded {
		t.Fatalf("expected the row threshold to win, got %s", status.Status)
	}
	if status.Threshold == nil || status.Threshold.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("expected row threshold 1, got %v", status.Threshold)
	}
}

func TestRefresh_GasStatus_MissingOrInvalidFallbackIsAlwaysSeeded(t *testing.T) {
	cases := []struct {
		name     string
		defaults map[string]GasReadinessDefault
	}{
		{name: "no entry", defaults: map[string]GasReadinessDefault{}},
		{name: "empty raw", defaults: map[string]GasReadinessDefault{"eth": {Raw: ""}}},
		{name: "invalid raw", defaults: map[string]GasReadinessDefault{"eth": {Raw: "not-a-number"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			walletID := uuid.New()
			baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
			wallet := &models.Wallet{
				ID:             walletID,
				Chain:          "eth",
				DepositAddress: &baseAddr,
				GasStatus:      models.GasStatusUnseeded,
			}
			mockChain := mocks.NewMockChain("eth")
			mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
				t.Fatalf("missing threshold must not call GetBalance (got addr=%s)", addr)
				return nil, errors.New("should not be called")
			}
			chainEntity := &models.Chain{
				ID:           "eth",
				AdapterType:  models.AdapterTypeEVM,
				NativeSymbol: "eth",
			}

			svc, _ := newGasReadinessService(t, wallet, mockChain, chainEntity)
			svc.gasDefaults = tc.defaults

			status, err := svc.RefreshGasStatus(context.Background(), walletID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status.Status != models.GasStatusSeeded {
				t.Fatalf("expected seeded, got %s", status.Status)
			}
			if status.Threshold != nil || status.NativeBalance != nil {
				t.Fatalf("expected no threshold check, got threshold=%v balance=%v", status.Threshold, status.NativeBalance)
			}
		})
	}
}

func TestNew_Service_CopiesGasDefaults(t *testing.T) {
	defaults := map[string]GasReadinessDefault{"eth": {Raw: "1"}}
	svc, ok := NewService(Deps{GasDefaults: defaults}).(*service)
	if !ok {
		t.Fatal("expected *service")
	}
	defaults["eth"] = GasReadinessDefault{Raw: "2"}
	if svc.gasDefaults["eth"].Raw != "1" {
		t.Fatalf("constructor must copy gas defaults, got %q", svc.gasDefaults["eth"].Raw)
	}
	if svc.gasDefaults == nil {
		t.Fatal("expected copied gas defaults")
	}
}

type recordingGasEvents struct {
	inside     *bool
	stagedIn   bool
	sent       int
	sentInside bool
	fail       bool
	walletID   uuid.UUID
	payload    interface{}
}

func (r *recordingGasEvents) EnqueueEvent(context.Context, uuid.UUID, types.EventType, interface{}) {
}

func (r *recordingGasEvents) StageWalletGasStatusChanged(_ context.Context, walletID uuid.UUID, data interface{}) (func(context.Context), error) {
	if r.inside != nil {
		r.stagedIn = *r.inside
	}
	r.walletID = walletID
	r.payload = data
	if r.fail {
		return nil, errors.New("insert webhook event")
	}
	return func(context.Context) {
		if r.inside != nil && *r.inside {
			r.sentInside = true
		}
		r.sent++
	}, nil
}

type enqueueOnlyEvents struct{}

func (enqueueOnlyEvents) EnqueueEvent(context.Context, uuid.UUID, types.EventType, interface{}) {
}

func TestRefresh_GasStatus_CommitsTheWalletAndGasStatusWebhookTogether(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}
	mockChain := gasMockChain("eth", big.NewInt(10_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")
	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)
	events := &recordingGasEvents{inside: &walletRepo.inside}
	svc.webhookSvc = events

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil || status == nil || status.Status != models.GasStatusSeeded {
		t.Fatalf("status %+v err %v", status, err)
	}
	if walletRepo.withins != 1 || !walletRepo.gasCheckInside || walletRepo.updateCalls != 1 {
		t.Fatalf("withins=%d inside=%v updates=%d", walletRepo.withins, walletRepo.gasCheckInside, walletRepo.updateCalls)
	}
	if got := walletRepo.lastUpdates["gas_status"]; got != models.GasStatusSeeded {
		t.Fatalf("gas_status %v", got)
	}
	if !events.stagedIn || events.sent != 1 || events.sentInside || events.walletID != walletID {
		t.Fatalf("stagedIn=%v sent=%d sentInside=%v wallet=%s", events.stagedIn, events.sent, events.sentInside, events.walletID)
	}
	payload, ok := events.payload.(map[string]interface{})
	if !ok || payload["wallet_id"] != walletID.String() || payload["new_status"] != models.GasStatusSeeded {
		t.Fatalf("payload %+v", events.payload)
	}
}

func TestRefresh_GasStatus_RollsBackTheWalletWhenTheGasStatusWebhookFails(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}
	mockChain := gasMockChain("eth", big.NewInt(10_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")
	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)
	events := &recordingGasEvents{inside: &walletRepo.inside, fail: true}
	svc.webhookSvc = events

	_, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err == nil {
		t.Fatal("want the webhook insert to fail the gas-status update")
	}
	if !events.stagedIn || events.sent != 0 || walletRepo.withins != 1 || walletRepo.updateCalls != 0 {
		t.Fatalf("stagedIn=%v sent=%d withins=%d updates=%d", events.stagedIn, events.sent, walletRepo.withins, walletRepo.updateCalls)
	}
}

func TestRefresh_GasStatus_RejectsAnEventWriterThatCannotJoinTheTransaction(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusUnseeded,
	}
	mockChain := gasMockChain("eth", big.NewInt(10_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")
	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)
	svc.webhookSvc = enqueueOnlyEvents{}

	_, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err == nil {
		t.Fatal("want a writer that cannot join the transaction to fail the update")
	}
	if walletRepo.withins != 1 || walletRepo.updateCalls != 0 {
		t.Fatalf("withins=%d updates=%d", walletRepo.withins, walletRepo.updateCalls)
	}
}

func TestRefresh_GasStatus_NoTransitionDoesNotWriteAWebhook(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "0xBASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		GasStatus:      models.GasStatusSeeded,
	}
	mockChain := gasMockChain("eth", big.NewInt(100_000_000_000_000_000))
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")
	svc, walletRepo := newGasReadinessService(t, wallet, mockChain, chainEntity)
	events := &recordingGasEvents{inside: &walletRepo.inside}
	svc.webhookSvc = events

	status, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err != nil || status.Status != models.GasStatusSeeded {
		t.Fatalf("status %+v err %v", status, err)
	}
	if walletRepo.withins != 0 || walletRepo.updateCalls != 1 || events.sent != 0 || events.stagedIn {
		t.Fatalf("withins=%d updates=%d sent=%d staged=%v", walletRepo.withins, walletRepo.updateCalls, events.sent, events.stagedIn)
	}
	if _, ok := walletRepo.lastUpdates["gas_status"]; ok {
		t.Fatalf("expected no gas_status write, got %+v", walletRepo.lastUpdates)
	}
}

func TestStage_Wallet_GasStatusChangedInsertsTheRowBeforeSending(t *testing.T) {
	walletID := uuid.New()
	events := &fakeWebhookEventRepo{}
	var sentBeforeInsert bool
	var sent int
	sender := newRecordingSender(t, func(types.WebhookMessage) {
		if len(events.created) == 0 {
			sentBeforeInsert = true
		}
		sent++
	})
	svc := webhook.NewService(webhook.Deps{
		SQS: sender,
		Configs: &fakeWebhookConfigRepo{configs: []models.WebhookConfig{{
			ID: uuid.New(), URL: "https://example.test/hooks", Secret: "s",
			Events: `{"wallet.gas_status.changed"}`, IsActive: true,
		}}},
		Events: events,
	})
	send, err := svc.StageWalletGasStatusChanged(context.Background(), walletID, map[string]interface{}{
		"wallet_id": walletID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.created) != 1 || sent != 0 {
		t.Fatalf("created=%d sent=%d before the closure", len(events.created), sent)
	}
	if events.created[0].TransactionID != nil || events.created[0].EventType != string(types.EventWalletGasStatusChanged) {
		t.Fatalf("transaction=%v type=%s", events.created[0].TransactionID, events.created[0].EventType)
	}
	send(context.Background())
	if sentBeforeInsert || sent != 1 {
		t.Fatalf("sentBeforeInsert=%v sent=%d", sentBeforeInsert, sent)
	}
}

func TestRefresh_GasStatus_WalletWithoutDepositAddressErrors(t *testing.T) {
	walletID := uuid.New()
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: nil,
	}
	chainEntity := evmChainWithThreshold("eth", "5000000000000000")
	svc, _ := newGasReadinessService(t, wallet, mocks.NewMockChain("eth"), chainEntity)

	_, err := svc.RefreshGasStatus(context.Background(), walletID)
	if err == nil {
		t.Fatal("expected error when wallet has no deposit address")
	}
}
