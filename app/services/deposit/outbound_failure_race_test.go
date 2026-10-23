package deposit

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	// outboundRaceBlock is where the mock chain mines every outbound transaction;
	// outboundRaceTip is three confirmations later, past the required ones.
	outboundRaceBlock = uint64(100)
	outboundRaceTip   = outboundRaceBlock + 2
	outboundRaceCode  = "onchain_reverted"
)

// sentMessages is what the webhook queue was sent. A withdrawal.failed message also
// records the status of its transaction, read outside any database transaction,
// when it was sent: failed means it was sent after the failure committed.
type sentMessages struct {
	mu           sync.Mutex
	events       []types.EventType
	statusAtSend []string
}

func (s *sentMessages) record(t *testing.T, msg types.WebhookMessage) {
	status := ""
	if msg.EventType == types.EventWithdrawalFailed {
		var row models.Transaction
		if err := facades.Orm().Query().Where("id = ?", msg.TransactionID).FirstOrFail(&row); err != nil {
			t.Errorf("read the transaction of a sent withdrawal.failed: %v", err)
		}
		status = row.Status
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, msg.EventType)
	if msg.EventType == types.EventWithdrawalFailed {
		s.statusAtSend = append(s.statusAtSend, status)
	}
}

func (s *sentMessages) sent(eventType types.EventType) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sent := range s.events {
		if sent == eventType {
			n++
		}
	}
	return n
}

// outboundRace is the deposit service over a mock chain that mines every outbound
// transaction in outboundRaceBlock, with the production withdrawal lifecycle
// publisher, a config subscribed to the outbound events, and a webhook queue that
// records what it is sent.
type outboundRace struct {
	svc       *Service
	adapter   *mocks.MockChain
	queue     *sentMessages
	publisher *withdrawalevents.Publisher
}

func newOutboundRace(t *testing.T) outboundRace {
	t.Helper()
	fixtures.TestDB(t)
	adapter := mocks.NewMockChain(models.ChainTETH)
	adapter.RequiredConfirmationsVal = 3
	adapter.GetTransactionBlockVal = outboundRaceBlock
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/outbound", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed), string(types.EventSweepConfirmed),
	})

	queue := &sentMessages{}
	sender := mocks.NewMockSender(t)
	sender.EXPECT().SendWebhook(mock.Anything, mock.Anything).Run(func(_ context.Context, msg types.WebhookMessage) {
		queue.record(t, msg)
	}).Return(nil).Maybe()
	webhookSvc := newQueuedWebhookSvc(sender)
	svc := newDepositSvc(registry, webhookSvc)
	publisher := newWithdrawalPublisherOver(webhookSvc)
	svc.SetWithdrawalConfirmations(publisher)
	return outboundRace{svc: svc, adapter: adapter, queue: queue, publisher: publisher}
}

// broadcast records an outbound transaction of txType and returns it as a tracker
// pass reads it: confirming, in no block yet. A withdrawal has its withdrawals row.
func (r outboundRace) broadcast(t *testing.T, txType string) (models.Transaction, *models.Withdrawal) {
	t.Helper()
	wallet := fixtures.InsertWallet(t, models.ChainTETH)
	if txType == models.TxTypeWithdrawal {
		tx, withdrawal := evmWithdrawal(t, wallet.ID)
		return reloadTransaction(t, tx.ID), &withdrawal
	}
	tx := fixtures.InsertTransaction(t, wallet.ID, nil, models.ChainTETH, txType, string(types.TxStatusConfirming), models.NativeETH, "1000", 0)
	return reloadTransaction(t, tx.ID), nil
}

// confirm is a pass that finds tx, as read, mined in outboundRaceBlock at
// outboundRaceTip.
func (r outboundRace) confirm(t *testing.T, tx models.Transaction) {
	t.Helper()
	if err := r.svc.applyConfirmations(context.Background(), r.adapter, outboundRaceTip, []models.Transaction{tx}); err != nil {
		t.Fatal(err)
	}
}

func (r outboundRace) fail(tx models.Transaction, block uint64) error {
	return r.svc.failOutbound(context.Background(), r.adapter, tx, outboundRaceCode, block)
}

// outboundEvents counts the outbox rows of the outbound events about subject.
func outboundEvents(t *testing.T, eventType types.EventType, subject uuid.UUID) int {
	t.Helper()
	return len(webhookEvents(t, string(eventType), subject))
}

// Two processes can read the same open outbound row (overlapping tracker passes,
// withdrawals:reconcile): the one that confirms it first wins. A failure found
// after it leaves the row and its withdrawal as confirmed, rolls its own writes back
// (the block it read, the released idempotency key), and publishes no
// withdrawal.failed.
func TestFail_Outbound_ATransactionConfirmedMeanwhileIsNeitherFailedNorReported(t *testing.T) {
	for _, txType := range outboundTxTypes {
		t.Run(txType, func(t *testing.T) {
			r := newOutboundRace(t)
			stale, withdrawal := r.broadcast(t, txType)
			r.confirm(t, stale)
			confirmed := reloadTransaction(t, stale.ID)

			err := r.fail(stale, outboundRaceBlock+1)

			if !errors.Is(err, models.ErrTransactionSettled) {
				t.Fatalf("err %v, want ErrTransactionSettled", err)
			}
			got := reloadTransaction(t, stale.ID)
			if got.Status != string(types.TxStatusConfirmed) || got.ErrorMessage != "" || got.BlockNumber != int64(outboundRaceBlock) ||
				got.ConfirmedAt == nil || idempotencyKeyOf(got) != idempotencyKeyOf(confirmed) {
				t.Fatalf("transaction %+v, want it left confirmed in block %d as the other process wrote it", got, outboundRaceBlock)
			}
			if withdrawal == nil {
				return
			}
			if w := reloadWithdrawal(t, withdrawal.ID); w.Status != models.WithdrawalStatusConfirmed || w.FailureReason != nil {
				t.Fatalf("withdrawal %s (%v), want confirmed", w.Status, w.FailureReason)
			}
			if failed, confirmedEvents := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID), outboundEvents(t, types.EventWithdrawalConfirmed, withdrawal.ID); failed != 0 || confirmedEvents != 1 {
				t.Fatalf("%d withdrawal.failed and %d withdrawal.confirmed, want only the withdrawal.confirmed", failed, confirmedEvents)
			}
			if sent := r.queue.sent(types.EventWithdrawalFailed); sent != 0 {
				t.Fatalf("withdrawal.failed sent %d times", sent)
			}
		})
	}
}

// The other way round: a confirmation found after another process failed the row
// leaves it failed and publishes no confirmation.
func TestApply_Confirmations_AnOutboundTransactionFailedMeanwhileIsNeitherConfirmedNorReported(t *testing.T) {
	for _, txType := range outboundTxTypes {
		t.Run(txType, func(t *testing.T) {
			r := newOutboundRace(t)
			stale, withdrawal := r.broadcast(t, txType)
			if err := r.fail(stale, outboundRaceBlock); err != nil {
				t.Fatal(err)
			}

			r.confirm(t, stale)

			got := reloadTransaction(t, stale.ID)
			if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != outboundRaceCode || got.ConfirmedAt != nil {
				t.Fatalf("status %s error %q confirmed_at %v, want it left failed with %s", got.Status, got.ErrorMessage, got.ConfirmedAt, outboundRaceCode)
			}
			if n := len(eventsOfType(t, types.EventSweepConfirmed)); n != 0 {
				t.Fatalf("%d sweep.confirmed for a failed transaction", n)
			}
			if withdrawal == nil {
				return
			}
			if w := reloadWithdrawal(t, withdrawal.ID); w.Status != models.WithdrawalStatusFailed || w.FailureReason == nil || *w.FailureReason != outboundRaceCode {
				t.Fatalf("withdrawal %s (%v), want failed with %s", w.Status, w.FailureReason, outboundRaceCode)
			}
			if failed, confirmed := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID), outboundEvents(t, types.EventWithdrawalConfirmed, withdrawal.ID); failed != 1 || confirmed != 0 {
				t.Fatalf("%d withdrawal.failed and %d withdrawal.confirmed, want only one withdrawal.failed", failed, confirmed)
			}
		})
	}
}

// Concurrent failures of one withdrawal: exactly one records it, withdrawal.failed
// is recorded and sent once, after the failure committed, and the others are told
// it was settled.
func TestFail_Outbound_ConcurrentFailuresRecordOneAndPublishWithdrawalFailedOnce(t *testing.T) {
	r := newOutboundRace(t)
	stale, withdrawal := r.broadcast(t, models.TxTypeWithdrawal)
	const callers = 8
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = r.fail(stale, outboundRaceBlock)
		}()
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, models.ErrTransactionSettled):
			t.Fatalf("a failure lost with %v, want ErrTransactionSettled", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d failures recorded, want exactly one", won)
	}
	if got := reloadTransaction(t, stale.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != outboundRaceCode {
		t.Fatalf("status %s error %q", got.Status, got.ErrorMessage)
	}
	if w := reloadWithdrawal(t, withdrawal.ID); w.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal %s, want failed", w.Status)
	}
	if n := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID); n != 1 {
		t.Fatalf("%d withdrawal.failed recorded, want one", n)
	}
	r.queue.mu.Lock()
	defer r.queue.mu.Unlock()
	if len(r.queue.statusAtSend) != 1 || r.queue.statusAtSend[0] != string(types.TxStatusFailed) {
		t.Fatalf("withdrawal.failed sent with its transaction %v, want once, after the failure committed", r.queue.statusAtSend)
	}
}

// A confirmation and a failure of one withdrawal racing: exactly one settles it, and
// only its event is published.
func TestFail_Outbound_RacingAConfirmationSettlesTheWithdrawalOnce(t *testing.T) {
	r := newOutboundRace(t)
	outcomes := map[string]int{}
	for round := range 20 {
		stale, withdrawal := r.broadcast(t, models.TxTypeWithdrawal)
		// Every other round the row is read once its block is recorded: the
		// confirmation then reaches its status write sooner, and either side can win.
		if round%2 == 1 {
			if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", stale.ID).Update("block_number", outboundRaceBlock); err != nil {
				t.Fatal(err)
			}
			stale = reloadTransaction(t, stale.ID)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var failErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			failErr = r.fail(stale, outboundRaceBlock)
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := r.svc.applyConfirmations(context.Background(), r.adapter, outboundRaceTip, []models.Transaction{stale}); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()

		got, w := reloadTransaction(t, stale.ID), reloadWithdrawal(t, withdrawal.ID)
		failed, confirmed := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID), outboundEvents(t, types.EventWithdrawalConfirmed, withdrawal.ID)
		outcomes[got.Status]++
		switch got.Status {
		case string(types.TxStatusConfirmed):
			if !errors.Is(failErr, models.ErrTransactionSettled) || w.Status != models.WithdrawalStatusConfirmed || failed != 0 || confirmed != 1 {
				t.Fatalf("confirmed: failure err %v, withdrawal %s, %d withdrawal.failed and %d withdrawal.confirmed", failErr, w.Status, failed, confirmed)
			}
		case string(types.TxStatusFailed):
			if failErr != nil || w.Status != models.WithdrawalStatusFailed || failed != 1 || confirmed != 0 {
				t.Fatalf("failed: failure err %v, withdrawal %s, %d withdrawal.failed and %d withdrawal.confirmed", failErr, w.Status, failed, confirmed)
			}
		default:
			t.Fatalf("status %s, want the withdrawal settled by one of them", got.Status)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// idempotencyKeyOf is the row's idempotency key, empty for none (a failure releases it).
func idempotencyKeyOf(tx models.Transaction) string {
	if tx.IdempotencyKey == nil {
		return ""
	}
	return *tx.IdempotencyKey
}

// stageThenFail is the production publisher whose StageFailed records
// withdrawal.failed and then fails, as a withdrawal row that cannot be written would.
type stageThenFail struct{ *withdrawalevents.Publisher }

func (p stageThenFail) StageFailed(ctx context.Context, tx *models.Transaction, failureCode string) (func(context.Context), error) {
	if _, err := p.Publisher.StageFailed(ctx, tx, failureCode); err != nil {
		return nil, err
	}
	return nil, errors.New("the withdrawal row was not written")
}

// The failed status, the block and withdrawal.failed are one database transaction:
// a failure that is not recorded whole leaves the row as the pass read it, with no
// withdrawal.failed, and the next pass records it.
func TestFail_Outbound_AFailureNotRecordedWholeLeavesTheRowForTheNextPass(t *testing.T) {
	r := newOutboundRace(t)
	stale, withdrawal := r.broadcast(t, models.TxTypeWithdrawal)
	r.svc.SetWithdrawalConfirmations(stageThenFail{r.publisher})

	err := r.fail(stale, outboundRaceBlock)

	if err == nil || errors.Is(err, models.ErrTransactionSettled) {
		t.Fatalf("err %v, want the staging failure", err)
	}
	if got := reloadTransaction(t, stale.ID); got.Status != stale.Status || got.ErrorMessage != "" || got.BlockNumber != 0 || idempotencyKeyOf(got) != idempotencyKeyOf(stale) {
		t.Fatalf("transaction %+v, want it as the pass read it", got)
	}
	if w := reloadWithdrawal(t, withdrawal.ID); w.Status != withdrawal.Status {
		t.Fatalf("withdrawal %s, want %s", w.Status, withdrawal.Status)
	}
	if n, sent := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID), r.queue.sent(types.EventWithdrawalFailed); n != 0 || sent != 0 {
		t.Fatalf("%d withdrawal.failed recorded and %d sent, want none", n, sent)
	}

	r.svc.SetWithdrawalConfirmations(r.publisher)
	if err := r.fail(stale, outboundRaceBlock); err != nil {
		t.Fatalf("next pass: %v", err)
	}
	if got := reloadTransaction(t, stale.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != int64(outboundRaceBlock) {
		t.Fatalf("next pass: status %s block %d, want failed in block %d", got.Status, got.BlockNumber, outboundRaceBlock)
	}
	if n := outboundEvents(t, types.EventWithdrawalFailed, withdrawal.ID); n != 1 {
		t.Fatalf("next pass: %d withdrawal.failed, want one", n)
	}
}

// withdrawals:reconcile applies a failure verdict through failOnChain: on a row
// another process confirmed after reconciliation read it, the verdict is not
// applied and the row is left confirmed.
func TestReconcile_Outbound_AFailureOnARowConfirmedMeanwhileIsNotApplied(t *testing.T) {
	for _, txType := range outboundTxTypes {
		t.Run(txType, func(t *testing.T) {
			r := newOutboundRace(t)
			stale, _ := r.broadcast(t, txType)
			r.confirm(t, stale)
			report := WithdrawalReconcileReport{Verdict: ReconcileFailed, FailureCode: outboundRaceCode, Block: outboundRaceBlock + 1}

			r.svc.applyReconciliation(context.Background(), r.adapter, stale, &report)

			if report.Applied {
				t.Fatalf("report %+v, want the failure not applied", report)
			}
			if got := reloadTransaction(t, stale.ID); got.Status != string(types.TxStatusConfirmed) || got.ErrorMessage != "" {
				t.Fatalf("status %s error %q, want it left confirmed", got.Status, got.ErrorMessage)
			}
		})
	}
}

// Without a withdrawal lifecycle publisher, the legacy withdrawal.failed is sent
// once, after the failure has committed.
func TestFail_Outbound_WithoutALifecyclePublisherSendsTheLegacyWithdrawalFailedAfterCommit(t *testing.T) {
	r := newOutboundRace(t)
	stale, _ := r.broadcast(t, models.TxTypeWithdrawal)
	r.svc.SetWithdrawalConfirmations(nil)

	if err := r.fail(stale, outboundRaceBlock); err != nil {
		t.Fatal(err)
	}

	if got := reloadTransaction(t, stale.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != outboundRaceCode {
		t.Fatalf("status %s error %q", got.Status, got.ErrorMessage)
	}
	r.queue.mu.Lock()
	defer r.queue.mu.Unlock()
	if len(r.queue.statusAtSend) != 1 || r.queue.statusAtSend[0] != string(types.TxStatusFailed) {
		t.Fatalf("withdrawal.failed sent with its transaction %v, want once, after the failure committed", r.queue.statusAtSend)
	}
}
