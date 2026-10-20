package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const utxoHead = uint64(5000)

// utxoChain is a Bitcoin-family chain whose failover order knows no transaction by
// its hash; search and spends answer what every provider and the spend index say.
type utxoChain struct {
	*mocks.MockChain
	mu       sync.Mutex
	search   map[string]uint64
	spends   map[string]chain.InputsSpend
	spendErr error
	proofs   map[string]int
}

func newUTXOChain() *utxoChain {
	mock := mocks.NewMockChain(models.ChainTBTC)
	mock.GetLatestBlockFn = func(context.Context) (uint64, error) { return utxoHead, nil }
	mock.GetTransactionBlockFn = func(context.Context, string) (uint64, error) { return 0, nil }
	return &utxoChain{MockChain: mock, search: map[string]uint64{}, spends: map[string]chain.InputsSpend{}, proofs: map[string]int{}}
}

func (c *utxoChain) SearchTransactionBlock(_ context.Context, txHash string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.search[txHash], nil
}

func (c *utxoChain) InputsSpend(_ context.Context, query chain.InputsQuery) (chain.InputsSpend, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.proofs[query.TxHash]++
	if len(query.Inputs) == 0 {
		return chain.InputsSpend{}, errors.New("no inputs")
	}
	if query.To != "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx" || query.Amount == nil || query.Amount.Int64() != 10000 {
		return chain.InputsSpend{}, fmt.Errorf("the proof is asked about %s %v, not what the withdrawal pays", query.To, query.Amount)
	}
	return c.spends[query.TxHash], c.spendErr
}

func (c *utxoChain) asked(txHash string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.proofs[txHash]
}

var utxoInputs = []string{strings.Repeat("c3", 32) + ":0", strings.Repeat("d4", 32) + ":1"}

// utxoWithdrawal is a Bitcoin withdrawal recorded with the inputs it spends (none
// when inputs is nil); unknown is a broadcast whose answer did not say whether the
// network took it.
func utxoWithdrawal(t *testing.T, walletID uuid.UUID, inputs []string, unknown bool) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx := fixtures.InsertTransaction(t, walletID, nil, models.ChainTBTC, models.TxTypeWithdrawal, "confirming", models.NativeBTC, "10000", 0)
	payload := map[string]any{}
	if inputs != nil {
		payload[chain.SpentInputsPayloadKey] = inputs
	}
	if unknown {
		payload[chain.BroadcastOutcomePayloadKey] = chain.BroadcastOutcomeUnknown
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	withdrawalID := uuid.New()
	const destination = "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"raw_payload": string(raw), "idempotency_key": withdrawalID.String(), "to_address": destination}); err != nil {
		t.Fatal(err)
	}
	tx.RawPayload, tx.ToAddress = string(raw), destination
	withdrawal := models.Withdrawal{
		ID: withdrawalID, WalletID: walletID, TransactionID: &tx.ID, Status: models.WithdrawalStatusBroadcast,
		Amount: "0.0001", DestinationAddress: destination, FeeEstimate: "0",
	}
	if err := facades.Orm().Query().Create(&withdrawal); err != nil {
		t.Fatal(err)
	}
	return tx, withdrawal
}

func TestRunConfirmationCheck_BitcoinUnknownOutcomesAreSettledByTheirInputs(t *testing.T) {
	fixtures.TestDB(t)
	utxo := newUTXOChain()
	registry := chain.NewRegistry()
	registry.RegisterChain(utxo)
	w := fixtures.InsertWallet(t, models.ChainTBTC)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/btc", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed),
	})

	conflictedTx, conflicted := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spends[conflictedTx.TxHash] = chain.InputsSpend{Conflicted: true, Block: utxoHead - 10}

	foundTx, found := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.search[foundTx.TxHash] = utxoHead - 20

	spentByUsTx, spentByUs := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spends[spentByUsTx.TxHash] = chain.InputsSpend{Ours: true, Block: utxoHead - 5, TxHash: spentByUsTx.TxHash}

	copiedTx, copied := utxoWithdrawal(t, w.ID, utxoInputs, true)
	const copyHash = "c0ffee0000000000000000000000000000000000000000000000000000000000"
	utxo.spends[copiedTx.TxHash] = chain.InputsSpend{Ours: true, Block: utxoHead - 7, TxHash: copyHash}

	openTx, open := utxoWithdrawal(t, w.ID, utxoInputs, true)

	mempoolTx, mempool := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spends[mempoolTx.TxHash] = chain.InputsSpend{Ours: true}

	// A broadcast the network accepted is never failed by this proof.
	acceptedTx, accepted := utxoWithdrawal(t, w.ID, utxoInputs, false)
	utxo.spends[acceptedTx.TxHash] = chain.InputsSpend{Conflicted: true, Block: utxoHead - 10}

	inputlessTx, inputless := utxoWithdrawal(t, w.ID, nil, true)
	utxo.spends[inputlessTx.TxHash] = chain.InputsSpend{Conflicted: true, Block: utxoHead - 10}

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	clock := &testClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	svc.now = clock.Now
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := reloadTransaction(t, conflictedTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != 0 || got.ErrorMessage != models.WithdrawalFailureNotLanded {
		t.Fatalf("every input spent elsewhere: status %s block %d error %q, want failed %s", got.Status, got.BlockNumber, got.ErrorMessage, models.WithdrawalFailureNotLanded)
	}
	if got := reloadWithdrawal(t, conflicted.ID); got.Status != models.WithdrawalStatusFailed {
		t.Fatalf("conflicted withdrawal %s, want failed", got.Status)
	}
	if got := webhookEvents(t, string(types.EventWithdrawalFailed), conflicted.ID); len(got) != 1 {
		t.Fatalf("%d withdrawal.failed events for the conflicted withdrawal, want 1", len(got))
	}
	for name, landed := range map[string]struct {
		tx         models.Transaction
		withdrawal models.Withdrawal
		block      int64
	}{
		"found on another provider":         {foundTx, found, int64(utxoHead) - 20},
		"its inputs spent by itself":        {spentByUsTx, spentByUs, int64(utxoHead) - 5},
		"carried by a copy paying the same": {copiedTx, copied, int64(utxoHead) - 7},
	} {
		if got := reloadTransaction(t, landed.tx.ID); got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != landed.block {
			t.Errorf("%s: status %s block %d, want confirmed in block %d", name, got.Status, got.BlockNumber, landed.block)
		}
		if got := reloadWithdrawal(t, landed.withdrawal.ID); got.Status != models.WithdrawalStatusConfirmed {
			t.Errorf("%s: withdrawal %s, want confirmed", name, got.Status)
		}
	}
	for name, pending := range map[string]struct {
		tx         models.Transaction
		withdrawal models.Withdrawal
	}{
		"nothing proves it yet":         {openTx, open},
		"it is still in the mempool":    {mempoolTx, mempool},
		"its broadcast was accepted":    {acceptedTx, accepted},
		"recorded without their inputs": {inputlessTx, inputless},
	} {
		if got := reloadTransaction(t, pending.tx.ID); got.Status != string(types.TxStatusConfirming) || got.ErrorMessage != "" {
			t.Errorf("%s: status %s error %q, want still confirming", name, got.Status, got.ErrorMessage)
		}
		if got := reloadWithdrawal(t, pending.withdrawal.ID); got.Status != models.WithdrawalStatusBroadcast {
			t.Errorf("%s: withdrawal %s, want broadcast", name, got.Status)
		}
	}
	var copiedPayload map[string]any
	_ = json.Unmarshal([]byte(reloadTransaction(t, copiedTx.ID).RawPayload), &copiedPayload)
	if copiedPayload[chain.LandedAsPayloadKey] != copyHash {
		t.Fatalf("raw_payload %v: the copy that carried the withdrawal is kept", copiedPayload)
	}
	if utxo.asked(acceptedTx.TxHash) != 0 || utxo.asked(inputlessTx.TxHash) != 0 {
		t.Fatal("only unknown outcomes recorded with their inputs are proven")
	}

	// A row nothing proved is asked again after the back-off, not on every pass.
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, mempooled := utxo.asked(openTx.TxHash), utxo.asked(mempoolTx.TxHash); got != 1 || mempooled != 1 {
		t.Fatalf("asked %d and %d times after a second pass, want 1 each", got, mempooled)
	}
	clock.advance(spendProofRetryAfter + time.Minute)
	utxo.spends[openTx.TxHash] = chain.InputsSpend{Conflicted: true, Block: utxoHead - 3}
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, openTx.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != models.WithdrawalFailureNotLanded {
		t.Fatalf("after the back-off: status %s error %q, want failed %s", got.Status, got.ErrorMessage, models.WithdrawalFailureNotLanded)
	}
}

func TestReconcile_Withdrawal_ProvesABitcoinTransferByItsInputs(t *testing.T) {
	fixtures.TestDB(t)
	utxo := newUTXOChain()
	registry := chain.NewRegistry()
	registry.RegisterChain(utxo)
	w := fixtures.InsertWallet(t, models.ChainTBTC)
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	svc.SetWithdrawalReconciliation(ormRows{}, ormTransactions{})
	ctx := context.Background()

	// Accepted or not, the operator's reconciliation reads any proof.
	conflictedTx, conflicted := utxoWithdrawal(t, w.ID, utxoInputs, false)
	utxo.spends[conflictedTx.TxHash] = chain.InputsSpend{Conflicted: true, Block: utxoHead - 10}
	report, err := svc.ReconcileWithdrawal(ctx, WithdrawalReconciliation{ID: conflicted.ID, Apply: true})
	if err != nil || report.Verdict != ReconcileFailed || report.FailureCode != models.WithdrawalFailureNotLanded || !report.Applied ||
		!strings.Contains(report.Reason, "inputs") {
		t.Fatalf("conflicted: %+v, %v", report, err)
	}
	if got := reloadWithdrawal(t, conflicted.ID); got.Status != models.WithdrawalStatusFailed {
		t.Fatalf("conflicted withdrawal %s, want failed", got.Status)
	}

	foundTx, found := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.search[foundTx.TxHash] = utxoHead - 20
	report, err = svc.ReconcileWithdrawal(ctx, WithdrawalReconciliation{ID: found.ID})
	if err != nil || report.Verdict != ReconcileLanded || report.Block != utxoHead-20 || report.Applied {
		t.Fatalf("found: %+v, %v", report, err)
	}

	copyTx, copied := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spends[copyTx.TxHash] = chain.InputsSpend{Ours: true, Block: utxoHead - 8, TxHash: strings.Repeat("ef", 32)}
	report, err = svc.ReconcileWithdrawal(ctx, WithdrawalReconciliation{ID: copied.ID})
	if err != nil || report.Verdict != ReconcileLanded || report.Block != utxoHead-8 || !strings.Contains(report.Reason, "copy") {
		t.Fatalf("a copy paying the same: %+v, %v", report, err)
	}

	mempoolTx, mempool := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spends[mempoolTx.TxHash] = chain.InputsSpend{Ours: true}
	report, err = svc.ReconcileWithdrawal(ctx, WithdrawalReconciliation{ID: mempool.ID, Apply: true})
	if err != nil || report.Verdict != ReconcileUnknown || !strings.Contains(report.Reason, "mempool") || report.Applied {
		t.Fatalf("in the mempool: %+v, %v", report, err)
	}

	unreadableTx, unreadable := utxoWithdrawal(t, w.ID, utxoInputs, true)
	utxo.spendErr = errors.New("operation not supported by this provider")
	report, err = svc.ReconcileWithdrawal(ctx, WithdrawalReconciliation{ID: unreadable.ID, Apply: true})
	if err != nil || report.Verdict != ReconcileUnknown || !strings.Contains(report.Reason, "inputs") {
		t.Fatalf("unreadable spends: %+v, %v", report, err)
	}
	if got := reloadTransaction(t, unreadableTx.ID); got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("unreadable spends: status %s, want confirming", got.Status)
	}
}
