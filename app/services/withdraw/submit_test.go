package withdraw

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/tests/memcache"
)

type submitRows struct {
	*memWithdrawalRows
	failed       map[uuid.UUID]string
	broadcast    map[uuid.UUID]*uuid.UUID
	markFailErr  error
	markBroadErr error
}

func newSubmitRows() *submitRows {
	return &submitRows{
		memWithdrawalRows: &memWithdrawalRows{},
		failed:            map[uuid.UUID]string{},
		broadcast:         map[uuid.UUID]*uuid.UUID{},
	}
}

func (r *submitRows) MarkFailed(_ context.Context, id uuid.UUID, reason string) error {
	if r.markFailErr != nil {
		return r.markFailErr
	}
	r.failed[id] = reason
	return nil
}

func (r *submitRows) MarkBroadcast(_ context.Context, id uuid.UUID, transactionID *uuid.UUID) error {
	if r.markBroadErr != nil {
		return r.markBroadErr
	}
	r.broadcast[id] = transactionID
	return nil
}

type submitEvents struct {
	broadcasts []*models.Transaction
	failures   []string
	attempts   []withdrawalevents.FailedAttempt
}

func (e *submitEvents) PublishBroadcast(_ context.Context, _ *models.Withdrawal, tx *models.Transaction) error {
	e.broadcasts = append(e.broadcasts, tx)
	return nil
}

func (e *submitEvents) PublishFailed(_ context.Context, _ *models.Withdrawal, code string, attempt withdrawalevents.FailedAttempt) error {
	e.failures = append(e.failures, code)
	e.attempts = append(e.attempts, attempt)
	return nil
}

// planSweep plans a withdrawal and executes it the way the test says.
type planSweep struct {
	mockSweepSvc
	strategy sweep.Strategy
	result   *sweep.Result
	planned  uuid.UUID
}

func (p *planSweep) PlanForWithdrawal(_ context.Context, walletID uuid.UUID, asset string, amount *big.Int, _ string, callerAccountID uuid.UUID) (*sweep.Plan, error) {
	p.planned = callerAccountID
	return &sweep.Plan{WalletID: walletID, Asset: asset, Amount: amount, Strategy: p.strategy}, nil
}

func (p *planSweep) ExecutePlan(context.Context, *sweep.Plan, sweep.SigningCredentials, uuid.UUID, string, string) (*sweep.Result, error) {
	return p.result, nil
}

type fixedWallet struct{ wallet *models.Wallet }

func (f fixedWallet) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return f.wallet, nil
}

func newSubmitService(t *testing.T, wallet *models.Wallet, runner *planSweep, rows *submitRows, events SubmitEvents) *Service {
	t.Helper()
	feeChain := newCreateChain(t, &fakeBroadcaster{}, "1", nil)
	svc := newCreateService(t, feeChain.chain, rows, acceptTotp{}, &memChains{decimals: 18})
	svc.transactionRepo = newMemTransactions()
	svc.walletRepo = fixedWallet{wallet: wallet}
	svc.sweep = runner
	svc.cache = memcache.New()
	svc.UseSubmit(rows, events)
	return svc
}

func submitInput(wallet *models.Wallet) SubmitInput {
	return SubmitInput{
		Wallet:             wallet,
		CallerUserID:       uuid.New(),
		TotpCode:           "000000",
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     uuid.New().String(),
	}
}

func TestSubmit_Refuses_ACallerWithNeitherUserNorAccount(t *testing.T) {
	rows := newSubmitRows()
	wallet := sealedCreateWallet(t, "eth", nil)
	svc := newSubmitService(t, wallet, &planSweep{}, rows, &submitEvents{})
	in := submitInput(wallet)
	in.CallerUserID = uuid.Nil

	_, err := svc.Submit(context.Background(), in)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v", err)
	}
	if rows.creates != 0 {
		t.Fatalf("row written for an unauthenticated caller: %d", rows.creates)
	}
}

func TestSubmit_Passes_ACreateRefusalThroughUnwrapped(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	svc := newSubmitService(t, wallet, &planSweep{}, newSubmitRows(), &submitEvents{})
	in := submitInput(wallet)
	in.IdempotencyKey = "not-a-uuid"

	_, err := svc.Submit(context.Background(), in)
	var refusal *CreateRefusal
	var executed *ExecuteError
	if !errors.As(err, &refusal) || errors.As(err, &executed) || refusal.Status != CreateStatusBadRequest {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmit_Wraps_AnUntypedCreateFailureAsARowError(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	svc := newSubmitService(t, wallet, &planSweep{}, newSubmitRows(), &submitEvents{})
	svc.createUsers = nil

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var row *CreateRowError
	if !errors.As(err, &row) || row.Endpoint != "create_wallet_withdrawal" || row.Err == nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmit_Keeps_AnUnknownChainRecognisable(t *testing.T) {
	wallet := sealedCreateWallet(t, "nope", nil)
	svc := newSubmitService(t, wallet, &planSweep{}, newSubmitRows(), &submitEvents{})

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var row *CreateRowError
	if !errors.Is(err, chainregistry.ErrUnknownChain) || errors.As(err, &row) {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmit_Replays_ABroadcastRowWithoutSigning(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	existingID := uuid.New()
	existing := &models.Withdrawal{ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusBroadcast, Amount: "1", DestinationAddress: "0xold"}
	rows := newSubmitRows()
	rows.byID = map[uuid.UUID]*models.Withdrawal{existingID: existing}
	events := &submitEvents{}
	svc := newSubmitService(t, wallet, &planSweep{}, rows, events)
	in := submitInput(wallet)
	in.IdempotencyKey = existingID.String()

	result, err := svc.Submit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Withdrawal != existing {
		t.Fatalf("result = %+v", result)
	}
	if len(rows.failed) != 0 || len(rows.broadcast) != 0 || len(events.broadcasts) != 0 || len(events.failures) != 0 {
		t.Fatal("a replay marked or published something")
	}
}

func TestSubmit_Marks_TheRowFailedAndPublishesWhenTheRequestFails(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	events := &submitEvents{}
	svc := newSubmitService(t, wallet, &planSweep{strategy: sweep.StrategyInsufficient}, rows, events)

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var executed *ExecuteError
	if !errors.As(err, &executed) || !errors.Is(err, sweep.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if len(rows.created) != 1 {
		t.Fatalf("rows created = %d", len(rows.created))
	}
	row := rows.created[0]
	if rows.failed[row.ID] != FailureInsufficientFunds || row.Status != models.WithdrawalStatusFailed {
		t.Fatalf("failed = %v status = %q", rows.failed, row.Status)
	}
	if len(events.failures) != 1 || events.failures[0] != FailureInsufficientFunds || events.attempts[0].Chain != "eth" || events.attempts[0].Asset != "ETH" {
		t.Fatalf("published = %v %+v", events.failures, events.attempts)
	}
	if len(rows.broadcast) != 0 || len(events.broadcasts) != 0 {
		t.Fatal("a failed request was marked broadcast")
	}
}

func TestSubmit_Reports_AFailedMarkAsARowErrorNotTheRequestError(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	rows.markFailErr = errors.New("db down")
	events := &submitEvents{}
	svc := newSubmitService(t, wallet, &planSweep{strategy: sweep.StrategyInsufficient}, rows, events)

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var row *CreateRowError
	var executed *ExecuteError
	if !errors.As(err, &row) || errors.As(err, &executed) || row.Endpoint != "mark_withdrawal_failed" {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, rows.markFailErr) || len(events.failures) != 0 {
		t.Fatalf("err = %v published = %v", err, events.failures)
	}
}

func TestSubmit_Broadcasts_AndPublishesOnSuccess(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	events := &submitEvents{}
	txID := uuid.New()
	final := &models.Transaction{ID: txID, TxHash: "0xhash"}
	runner := &planSweep{strategy: sweep.StrategyDirectFromBase, result: &sweep.Result{FinalWithdrawTx: final}}
	svc := newSubmitService(t, wallet, runner, rows, events)

	result, err := svc.Submit(context.Background(), submitInput(wallet))
	if err != nil {
		t.Fatal(err)
	}
	w := result.Withdrawal
	if result.Replayed || w.Status != models.WithdrawalStatusBroadcast || w.TxHash != "0xhash" || w.TransactionID == nil || *w.TransactionID != txID {
		t.Fatalf("result = %+v withdrawal = %+v", result, w)
	}
	if got := rows.broadcast[w.ID]; got == nil || *got != txID {
		t.Fatalf("broadcast mark = %v", rows.broadcast)
	}
	if len(events.broadcasts) != 1 || events.broadcasts[0] != final {
		t.Fatalf("broadcasts = %v", events.broadcasts)
	}
}

func TestSubmit_Reports_AFailedBroadcastMarkAsARowError(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	rows.markBroadErr = errors.New("db down")
	events := &submitEvents{}
	runner := &planSweep{strategy: sweep.StrategyDirectFromBase, result: &sweep.Result{FinalWithdrawTx: &models.Transaction{ID: uuid.New(), TxHash: "0xhash"}}}
	svc := newSubmitService(t, wallet, runner, rows, events)

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var row *CreateRowError
	if !errors.As(err, &row) || row.Endpoint != "persist_broadcast_withdrawal" || len(events.broadcasts) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmit_Plans_WithTheWalletAccountWhenTheCallerHasNone(t *testing.T) {
	accountID := uuid.New()
	wallet := sealedCreateWallet(t, "eth", &accountID)
	runner := &planSweep{strategy: sweep.StrategyInsufficient}
	svc := newSubmitService(t, wallet, runner, newSubmitRows(), &submitEvents{})

	_, _ = svc.Submit(context.Background(), submitInput(wallet))
	if runner.planned != accountID {
		t.Fatalf("planned for %s, want %s", runner.planned, accountID)
	}

	own := uuid.New()
	in := submitInput(wallet)
	in.CallerAccountID = own
	_, _ = svc.Submit(context.Background(), in)
	if runner.planned != own {
		t.Fatalf("planned for %s, want the caller's %s", runner.planned, own)
	}
}

func TestSubmit_Works_WithoutAnEventPublisher(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	svc := newSubmitService(t, wallet, &planSweep{strategy: sweep.StrategyInsufficient}, rows, nil)

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	if !errors.Is(err, sweep.ErrInsufficientFunds) || len(rows.failed) != 1 {
		t.Fatalf("err = %v failed = %v", err, rows.failed)
	}
}
