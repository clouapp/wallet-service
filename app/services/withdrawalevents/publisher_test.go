package withdrawalevents

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	usdcAsset      = "USDC"
	usdcDecimals   = 6
	polygonChain   = "polygon"
	usdcBaseUnits  = "3000000"
	usdcHuman      = "3"
	storedDecimal  = "3.000000000000000000"
	testTxHash     = "0x05327dc610dea268d2576e635cb51627ef974bcb0dcca698f4c9d7fc5aaf6954"
	usdcContract   = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	destination    = "0x1111111111111111111111111111111111111111"
	requiredConfs  = 128
	confirmedConfs = 130
)

type fakeEnqueuer struct {
	events []webhook.ScopedEvent
	err    error
}

func (f *fakeEnqueuer) EnqueueScoped(ctx context.Context, event webhook.ScopedEvent) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.events = append(f.events, event)
	return 1, nil
}

type fakeWithdrawals struct {
	byTransaction map[uuid.UUID]*models.Withdrawal
	byID          map[uuid.UUID]*models.Withdrawal
	toBackfill    []models.Withdrawal
	updates       map[uuid.UUID]map[string]any
}

func newFakeWithdrawals() *fakeWithdrawals {
	return &fakeWithdrawals{
		byTransaction: map[uuid.UUID]*models.Withdrawal{},
		byID:          map[uuid.UUID]*models.Withdrawal{},
		updates:       map[uuid.UUID]map[string]any{},
	}
}

func (f *fakeWithdrawals) FindByTransactionID(transactionID uuid.UUID) (*models.Withdrawal, error) {
	return f.byTransaction[transactionID], nil
}

func (f *fakeWithdrawals) FindByIDAndWallet(withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	w := f.byID[withdrawalID]
	if w == nil || w.WalletID != walletID {
		return nil, nil
	}
	return w, nil
}

func (f *fakeWithdrawals) FindBroadcastWithConfirmedTransaction(limit int) ([]models.Withdrawal, error) {
	return f.toBackfill, nil
}

func (f *fakeWithdrawals) UpdateFields(id uuid.UUID, fields map[string]any) error {
	f.updates[id] = fields
	return nil
}

type fakeTransactions map[uuid.UUID]*models.Transaction

func (f fakeTransactions) FindByID(id uuid.UUID) (*models.Transaction, error) { return f[id], nil }

type fakeWallets map[uuid.UUID]*models.Wallet

func (f fakeWallets) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	return f[id], nil
}

type fakeDecimals map[string]int

func (f fakeDecimals) Decimals(chainID, asset string) (int, bool) {
	d, ok := f[chainID+"/"+asset]
	return d, ok
}

type fixture struct {
	publisher   *Publisher
	enqueuer    *fakeEnqueuer
	withdrawals *fakeWithdrawals
	accountID   uuid.UUID
	wallet      *models.Wallet
	withdrawal  *models.Withdrawal
	tx          *models.Transaction
}

func newFixture() *fixture {
	accountID := uuid.New()
	wallet := &models.Wallet{ID: uuid.New(), Chain: polygonChain, AccountID: &accountID}
	tx := &models.Transaction{
		ID:            uuid.New(),
		WalletID:      wallet.ID,
		Chain:         polygonChain,
		TxType:        models.TxTypeWithdrawal,
		TxHash:        testTxHash,
		ToAddress:     destination,
		Amount:        usdcBaseUnits,
		Asset:         usdcAsset,
		TokenContract: usdcContract,
		RequiredConfs: requiredConfs,
		Status:        string(types.TxStatusConfirming),
	}
	withdrawal := &models.Withdrawal{
		ID:                 uuid.New(),
		WalletID:           wallet.ID,
		TransactionID:      &tx.ID,
		AccountID:          &accountID,
		Status:             models.WithdrawalStatusBroadcast,
		Amount:             storedDecimal,
		DestinationAddress: destination,
	}
	idempotencyKey := withdrawal.ID.String()
	tx.IdempotencyKey = &idempotencyKey

	withdrawals := newFakeWithdrawals()
	withdrawals.byTransaction[tx.ID] = withdrawal
	withdrawals.byID[withdrawal.ID] = withdrawal

	enqueuer := &fakeEnqueuer{}
	publisher := NewPublisher(
		enqueuer,
		withdrawals,
		fakeTransactions{tx.ID: tx},
		fakeWallets{wallet.ID: wallet},
		fakeDecimals{polygonChain + "/" + usdcAsset: usdcDecimals},
	)
	publisher.now = func() time.Time { return time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC) }

	return &fixture{publisher, enqueuer, withdrawals, accountID, wallet, withdrawal, tx}
}

func (f *fixture) confirmTx() {
	f.tx.Status = string(types.TxStatusConfirmed)
	f.tx.Confirmations = confirmedConfs
}

func onlyPayload(t *testing.T, enqueuer *fakeEnqueuer) (webhook.ScopedEvent, Payload) {
	t.Helper()
	if len(enqueuer.events) != 1 {
		t.Fatalf("expected exactly one event, got %d", len(enqueuer.events))
	}
	event := enqueuer.events[0]
	payload, ok := event.Data.(Payload)
	if !ok {
		t.Fatalf("event data is %T, want Payload", event.Data)
	}
	return event, payload
}

func TestPublishBroadcast_PayloadCarriesBothAmountsAndScope(t *testing.T) {
	f := newFixture()

	if err := f.publisher.PublishBroadcast(context.Background(), f.withdrawal, f.tx); err != nil {
		t.Fatalf("PublishBroadcast: %v", err)
	}

	event, payload := onlyPayload(t, f.enqueuer)
	if event.EventType != types.EventWithdrawalBroadcast || string(event.EventType) != "withdrawal.broadcast" {
		t.Fatalf("event type = %s", event.EventType)
	}
	if event.SubjectID != f.withdrawal.ID.String() || event.WalletID != f.wallet.ID || event.AccountID == nil || *event.AccountID != f.accountID {
		t.Fatalf("event scope = %+v", event)
	}
	if payload.WithdrawalID != f.withdrawal.ID.String() || payload.IdempotencyKey != f.withdrawal.ID.String() {
		t.Fatalf("ids = %s / %s", payload.WithdrawalID, payload.IdempotencyKey)
	}
	if payload.Amount != usdcHuman || payload.AmountBaseUnits == nil || *payload.AmountBaseUnits != usdcBaseUnits {
		t.Fatalf("amounts = %q / %v", payload.Amount, payload.AmountBaseUnits)
	}
	if payload.Decimals == nil || *payload.Decimals != usdcDecimals {
		t.Fatalf("decimals = %v", payload.Decimals)
	}
	if payload.TxHash == nil || *payload.TxHash != testTxHash || payload.Status != models.WithdrawalStatusBroadcast {
		t.Fatalf("tx hash / status = %v / %s", payload.TxHash, payload.Status)
	}
	if payload.TokenContract == nil || *payload.TokenContract != usdcContract || payload.Asset != usdcAsset {
		t.Fatalf("asset = %s / %v", payload.Asset, payload.TokenContract)
	}
	if payload.RequiredConfirmations != requiredConfs || payload.FailureCode != nil {
		t.Fatalf("confirmations / failure = %d / %v", payload.RequiredConfirmations, payload.FailureCode)
	}
	if payload.OccurredAt != "2026-10-01T02:00:00Z" {
		t.Fatalf("occurred_at = %s", payload.OccurredAt)
	}
}

func TestPublishBroadcast_RequiresTransactionHash(t *testing.T) {
	f := newFixture()
	f.tx.TxHash = " "

	if err := f.publisher.PublishBroadcast(context.Background(), f.withdrawal, f.tx); err == nil {
		t.Fatal("expected an error for a transaction without hash")
	}
	if len(f.enqueuer.events) != 0 {
		t.Fatal("no event may be enqueued without a hash")
	}
}

func TestPublishFailed_SendsOnlyThePublicCode(t *testing.T) {
	f := newFixture()
	f.withdrawal.TransactionID = nil
	f.withdrawal.Status = models.WithdrawalStatusFailed
	units, _ := new(big.Int).SetString(usdcBaseUnits, 10)

	err := f.publisher.PublishFailed(context.Background(), f.withdrawal, "insufficient_funds", FailedAttempt{
		Chain: polygonChain, Asset: usdcAsset, BaseUnits: units,
	})
	if err != nil {
		t.Fatalf("PublishFailed: %v", err)
	}

	event, payload := onlyPayload(t, f.enqueuer)
	if event.EventType != types.EventWithdrawalFailed || event.TransactionID != nil {
		t.Fatalf("event = %+v", event)
	}
	if payload.FailureCode == nil || *payload.FailureCode != "insufficient_funds" {
		t.Fatalf("failure code = %v", payload.FailureCode)
	}
	if payload.TxHash != nil || payload.TransactionID != nil || payload.Status != models.WithdrawalStatusFailed {
		t.Fatalf("failed payload must have no transaction: %+v", payload)
	}
	if payload.Amount != usdcHuman || *payload.AmountBaseUnits != usdcBaseUnits {
		t.Fatalf("amounts = %q / %v", payload.Amount, payload.AmountBaseUnits)
	}
}

func TestPublishFailed_RejectsEmptyCodeAndMissingWithdrawal(t *testing.T) {
	f := newFixture()

	if err := f.publisher.PublishFailed(context.Background(), f.withdrawal, "  ", FailedAttempt{}); err == nil {
		t.Fatal("expected an error for an empty failure code")
	}
	if err := f.publisher.PublishFailed(context.Background(), nil, "internal_error", FailedAttempt{}); err == nil {
		t.Fatal("expected an error for a nil withdrawal")
	}
	if len(f.enqueuer.events) != 0 {
		t.Fatal("no event may be enqueued for invalid input")
	}
}

func TestPublishFailed_UnknownDecimalsFallsBackToRequestedAmount(t *testing.T) {
	f := newFixture()
	units, _ := new(big.Int).SetString(usdcBaseUnits, 10)

	err := f.publisher.PublishFailed(context.Background(), f.withdrawal, "internal_error", FailedAttempt{
		Chain: polygonChain, Asset: "UNKNOWN", BaseUnits: units,
	})
	if err != nil {
		t.Fatalf("PublishFailed: %v", err)
	}
	_, payload := onlyPayload(t, f.enqueuer)
	if payload.Amount != usdcHuman || payload.Decimals != nil {
		t.Fatalf("expected requested amount without decimals, got %q / %v", payload.Amount, payload.Decimals)
	}
}

func TestMarkConfirmed_PublishesThenMarksWithdrawalConfirmed(t *testing.T) {
	f := newFixture()
	f.confirmTx()

	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatalf("MarkConfirmed: %v", err)
	}

	event, payload := onlyPayload(t, f.enqueuer)
	if event.EventType != types.EventWithdrawalConfirmed || payload.Status != models.WithdrawalStatusConfirmed {
		t.Fatalf("event = %s / %s", event.EventType, payload.Status)
	}
	if payload.Confirmations != confirmedConfs || payload.IdempotencyKey != f.withdrawal.ID.String() {
		t.Fatalf("payload = %+v", payload)
	}
	update := f.withdrawals.updates[f.withdrawal.ID]
	if update["status"] != models.WithdrawalStatusConfirmed || update["transaction_id"] != f.tx.ID {
		t.Fatalf("withdrawal update = %+v", update)
	}
}

func TestMarkConfirmed_LeavesWithdrawalUntouchedWhenPublishingFails(t *testing.T) {
	f := newFixture()
	f.confirmTx()
	f.enqueuer.err = errors.New("database down")

	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err == nil {
		t.Fatal("expected the enqueue error")
	}
	if _, updated := f.withdrawals.updates[f.withdrawal.ID]; updated {
		t.Fatal("the withdrawal must stay broadcast so the backfill retries it")
	}
}

func TestMarkConfirmed_FindsWithdrawalByIdempotencyKeyWhenNotLinked(t *testing.T) {
	f := newFixture()
	f.confirmTx()
	delete(f.withdrawals.byTransaction, f.tx.ID)
	f.withdrawal.TransactionID = nil
	f.withdrawal.Status = "broadcasting"

	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatalf("MarkConfirmed: %v", err)
	}
	_, payload := onlyPayload(t, f.enqueuer)
	if payload.WithdrawalID != f.withdrawal.ID.String() {
		t.Fatalf("withdrawal id = %s", payload.WithdrawalID)
	}
	if f.withdrawals.updates[f.withdrawal.ID]["transaction_id"] != f.tx.ID {
		t.Fatal("the withdrawal must be linked to its transaction")
	}
}

func TestMarkConfirmed_RejectsTransactionsThatAreNotConfirmedWithdrawals(t *testing.T) {
	f := newFixture()

	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err == nil {
		t.Fatal("a confirming transaction must be rejected")
	}
	f.confirmTx()
	f.tx.TxType = models.TxTypeDeposit
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err == nil {
		t.Fatal("a deposit must be rejected")
	}
	if err := f.publisher.MarkConfirmed(context.Background(), nil); err == nil {
		t.Fatal("a nil transaction must be rejected")
	}
	if len(f.enqueuer.events) != 0 {
		t.Fatal("no event may be enqueued")
	}
}

func TestBackfill_ConfirmsBroadcastWithdrawalsWithConfirmedTransactions(t *testing.T) {
	f := newFixture()
	f.confirmTx()
	unlinked := models.Withdrawal{ID: uuid.New(), WalletID: f.wallet.ID}
	f.withdrawals.toBackfill = []models.Withdrawal{*f.withdrawal, unlinked}

	confirmed, err := f.publisher.Backfill(context.Background(), 10)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if confirmed != 1 {
		t.Fatalf("confirmed = %d, want 1", confirmed)
	}
	onlyPayload(t, f.enqueuer)

	if _, err := f.publisher.Backfill(context.Background(), 0); err == nil {
		t.Fatal("a non-positive limit must be rejected")
	}
}
