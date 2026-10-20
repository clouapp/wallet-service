package withdraw

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
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
	strategy  sweep.Strategy
	shortfall error
	planErr   error
	result    *sweep.Result
	planned   uuid.UUID
	plannedTo sweep.Destination
}

func (p *planSweep) PlanForWithdrawalTo(_ context.Context, walletID uuid.UUID, asset string, amount *big.Int, to sweep.Destination, callerAccountID uuid.UUID) (*sweep.Plan, error) {
	p.planned = callerAccountID
	p.plannedTo = to
	if p.planErr != nil {
		return nil, p.planErr
	}
	return &sweep.Plan{WalletID: walletID, Asset: asset, Amount: amount, Strategy: p.strategy, Shortfall: p.shortfall}, nil
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

func TestSubmit_Returns_TheXRPBaseShortfallAsInsufficientFunds(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	events := &submitEvents{}
	shortfall := &sweep.BaseReserveShortfallError{Address: "rBase", Amount: big.NewInt(9_000_000)}
	svc := newSubmitService(t, wallet, &planSweep{strategy: sweep.StrategyInsufficient, shortfall: shortfall}, rows, events)

	_, err := svc.Submit(context.Background(), submitInput(wallet))
	var got *sweep.BaseReserveShortfallError
	if !errors.As(err, &got) || got != shortfall || !errors.Is(err, sweep.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if row := rows.created[0]; rows.failed[row.ID] != FailureInsufficientFunds {
		t.Fatalf("failed = %v", rows.failed)
	}
	if len(events.failures) != 1 || events.failures[0] != FailureInsufficientFunds {
		t.Fatalf("published = %v", events.failures)
	}
}

func TestSubmit_Marks_AnXRPDestinationRefusalWithItsOwnCode(t *testing.T) {
	for planErr, code := range map[error]string{
		sweep.ErrDestinationBelowReserve: FailureDestinationReserve,
		sweep.ErrDestinationTagRequired:  FailureDestinationTag,
	} {
		wallet := sealedCreateWallet(t, "eth", nil)
		rows := newSubmitRows()
		events := &submitEvents{}
		svc := newSubmitService(t, wallet, &planSweep{planErr: planErr}, rows, events)

		_, err := svc.Submit(context.Background(), submitInput(wallet))
		if !errors.Is(err, planErr) {
			t.Fatalf("err = %v", err)
		}
		if row := rows.created[0]; rows.failed[row.ID] != code || len(events.failures) != 1 || events.failures[0] != code {
			t.Fatalf("%s: failed = %v published = %v", code, rows.failed, events.failures)
		}
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

func TestSubmit_Carries_TheXRPDestinationTagToTheRowAndThePlanner(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	runner := &planSweep{strategy: sweep.StrategyDirectFromBase, result: &sweep.Result{FinalWithdrawTx: &models.Transaction{ID: uuid.New(), TxHash: "ABC"}}}
	svc := newSubmitService(t, wallet, runner, rows, &submitEvents{})
	svc.createChains = &memChains{decimals: 6, adapterType: models.AdapterTypeXRP}
	in := submitInput(wallet)
	tag := uint32(4294967295)
	in.DestinationTag = &tag

	result, err := svc.Submit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if runner.plannedTo.Tag == nil || *runner.plannedTo.Tag != tag || runner.plannedTo.Address != in.DestinationAddress {
		t.Fatalf("planned to %+v", runner.plannedTo)
	}
	if len(rows.created) != 1 || rows.created[0].DestinationTag == nil || *rows.created[0].DestinationTag != tag {
		t.Fatalf("row %+v", rows.created)
	}
	if result.Withdrawal.DestinationTag == nil || *result.Withdrawal.DestinationTag != tag {
		t.Fatalf("withdrawal %+v", result.Withdrawal)
	}
}

func TestSubmit_Refuses_ADestinationTagOnAChainOtherThanXRP(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	runner := &planSweep{strategy: sweep.StrategyDirectFromBase}
	svc := newSubmitService(t, wallet, runner, rows, &submitEvents{})
	svc.createChains = &memChains{decimals: 18, adapterType: models.AdapterTypeEVM}
	in := submitInput(wallet)
	tag := uint32(5)
	in.DestinationTag = &tag

	_, err := svc.Submit(context.Background(), in)
	var refusal *CreateRefusal
	if !errors.As(err, &refusal) || refusal.Status != CreateStatusUnprocessable || refusal.Message != FailureDestinationTagUnsupported {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, sweep.ErrDestinationTagUnsupported) || rows.creates != 0 || runner.plannedTo.Address != "" {
		t.Fatalf("a refused tag wrote %d rows or reached the planner", rows.creates)
	}
}

func TestSubmit_Retries_AFailedRowWithTheNewDestinationTag(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	existingID := uuid.New()
	reason := FailureExpired
	rows := newSubmitRows()
	rows.byID = map[uuid.UUID]*models.Withdrawal{existingID: {ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusFailed, FailureReason: &reason}}
	runner := &planSweep{strategy: sweep.StrategyDirectFromBase, result: &sweep.Result{FinalWithdrawTx: &models.Transaction{ID: uuid.New(), TxHash: "ABC"}}}
	svc := newSubmitService(t, wallet, runner, rows, &submitEvents{})
	svc.createChains = &memChains{decimals: 6, adapterType: models.AdapterTypeXRP}
	in := submitInput(wallet)
	in.IdempotencyKey = existingID.String()
	tag := uint32(12)
	in.DestinationTag = &tag

	if _, err := svc.Submit(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if rows.retries != 1 || rows.retryTag == nil || *rows.retryTag != tag {
		t.Fatalf("retries %d tag %v", rows.retries, rows.retryTag)
	}
}

func TestSubmit_Retry_SendsAgainWhenTheKeysTransactionFailedOnChain(t *testing.T) {
	for status, wantNew := range map[string]bool{
		string(types.TxStatusFailed):     true,
		string(types.TxStatusConfirming): false,
	} {
		wallet := sealedCreateWallet(t, "eth", nil)
		existingID := uuid.New()
		reason := FailureOnChainPrefix + "tecno_dst_insuf_xrp"
		rows := newSubmitRows()
		rows.byID = map[uuid.UUID]*models.Withdrawal{existingID: {ID: existingID, WalletID: wallet.ID, Status: models.WithdrawalStatusFailed, FailureReason: &reason}}
		fresh := &models.Transaction{ID: uuid.New(), TxHash: "NEW"}
		runner := &planSweep{strategy: sweep.StrategyDirectFromBase, result: &sweep.Result{FinalWithdrawTx: fresh}}
		svc := newSubmitService(t, wallet, runner, rows, &submitEvents{})
		old := &models.Transaction{ID: uuid.New(), TxHash: "OLD", Status: status}
		txs := newMemTransactions()
		txs.byKey = map[string]*models.Transaction{existingID.String(): old}
		svc.transactionRepo = txs
		in := submitInput(wallet)
		in.IdempotencyKey = existingID.String()

		result, err := svc.Submit(context.Background(), in)
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if gotNew := result.Withdrawal.TxHash == "NEW"; gotNew != wantNew {
			t.Fatalf("existing transaction %s: withdrawal now carries %s", status, result.Withdrawal.TxHash)
		}
	}
}

// sendingSweep executes a plan as the sweep executor does: it signs, records the
// final transfer under the plan's idempotency key and links the plan's withdrawal
// to it before the bytes leave, then sends. It counts the transfers it signed.
type sendingSweep struct {
	planSweep
	txs    *memTransactions
	rows   *submitRows
	signed int
}

func (s *sendingSweep) ExecutePlan(_ context.Context, plan *sweep.Plan, _ sweep.SigningCredentials, txID uuid.UUID, _, _ string) (*sweep.Result, error) {
	s.signed++
	if plan.IdempotencyKey == "" || plan.WithdrawalID == uuid.Nil {
		return nil, errors.New("the plan names no withdrawal: the transfer could not be found by a retry")
	}
	key := plan.IdempotencyKey
	// The executor records what the send answered once the bytes left.
	tx := &models.Transaction{ID: txID, TxHash: "0xsent", Status: string(types.TxStatusConfirming), IdempotencyKey: &key,
		RawPayload: `{"broadcast_outcome":"accepted"}`}
	if s.txs.byKey == nil {
		s.txs.byKey = map[string]*models.Transaction{}
	}
	s.txs.byKey[key] = tx
	if withdrawal := s.rows.byID[plan.WithdrawalID]; withdrawal != nil {
		withdrawal.TransactionID = &tx.ID
	}
	return &sweep.Result{FinalWithdrawTx: tx}, nil
}

func TestSubmit_Retry_AfterACrashBetweenTheSendAndItsMarkNeverSignsAgain(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	events := &submitEvents{}
	txs := newMemTransactions()
	runner := &sendingSweep{planSweep: planSweep{strategy: sweep.StrategyDirectFromBase}, txs: txs, rows: rows}
	svc := newSubmitService(t, wallet, &runner.planSweep, rows, events)
	svc.sweep = runner
	svc.transactionRepo = txs
	in := submitInput(wallet)

	// The process sent the transfer, then could not mark the withdrawal broadcast
	// (it crashed, or the database failed): the withdrawal stays broadcasting.
	rows.markBroadErr = errors.New("db down")
	if _, err := svc.Submit(context.Background(), in); err == nil {
		t.Fatal("setup: the mark failed")
	}
	withdrawalID := uuid.MustParse(in.IdempotencyKey)
	stored := rows.byID[withdrawalID]
	if stored == nil || stored.TransactionID == nil {
		t.Fatalf("withdrawal %+v: linked to the transfer sent", stored)
	}
	// The in-memory row is the object Submit changed; the database kept what the
	// failed mark did not overwrite.
	stored.Status, stored.TxHash = models.WithdrawalStatusBroadcasting, ""
	if len(rows.failed) != 0 || len(events.failures) != 0 {
		t.Fatalf("failed=%v events=%v: a transfer that left is never failed", rows.failed, events.failures)
	}

	rows.markBroadErr = nil
	result, err := svc.Submit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if runner.signed != 1 {
		t.Fatalf("signed %d transfers: the retry with the same key must find the first one, never pay twice", runner.signed)
	}
	if w := result.Withdrawal; w.Status != models.WithdrawalStatusBroadcast || w.TxHash != "0xsent" {
		t.Fatalf("retry result %+v: the transfer sent the first time", w)
	}
}

// holdingSweep sends like sendingSweep, but holds the send until release closes.
type holdingSweep struct {
	sendingSweep
	started chan struct{}
	release chan struct{}
}

func (h *holdingSweep) ExecutePlan(ctx context.Context, plan *sweep.Plan, creds sweep.SigningCredentials, txID uuid.UUID, to, user string) (*sweep.Result, error) {
	result, err := h.sendingSweep.ExecutePlan(ctx, plan, creds, txID, to, user)
	close(h.started)
	<-h.release
	return result, err
}

func TestSubmit_SameKey_ASecondSubmitWaitsForTheFirstsOutcome(t *testing.T) {
	wallet := sealedCreateWallet(t, "eth", nil)
	rows := newSubmitRows()
	txs := newMemTransactions()
	runner := &holdingSweep{
		sendingSweep: sendingSweep{planSweep: planSweep{strategy: sweep.StrategyDirectFromBase}, txs: txs, rows: rows},
		started:      make(chan struct{}), release: make(chan struct{}),
	}
	svc := newSubmitService(t, wallet, &runner.planSweep, rows, &submitEvents{})
	svc.sweep = runner
	svc.transactionRepo = txs
	in := submitInput(wallet)

	first := make(chan error, 1)
	go func() {
		_, err := svc.Submit(context.Background(), in)
		first <- err
	}()
	<-runner.started // the first submit recorded its transfer and holds the send

	second := make(chan *SubmitResult, 1)
	secondErr := make(chan error, 1)
	go func() {
		result, err := svc.Submit(context.Background(), in)
		second <- result
		secondErr <- err
	}()
	select {
	case <-second:
		t.Fatal("the second submit with the same key answered before the first one's bytes left")
	case <-time.After(300 * time.Millisecond):
	}
	close(runner.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	result := <-second
	if err := <-secondErr; err != nil {
		t.Fatal(err)
	}
	if runner.signed != 1 || !result.Replayed || result.Withdrawal.TxHash != "0xsent" {
		t.Fatalf("signed %d, second %+v: the second submit replays the first one's outcome", runner.signed, result)
	}
}
