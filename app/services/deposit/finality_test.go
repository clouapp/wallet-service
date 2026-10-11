package deposit

import (
	"context"
	"errors"
	"math/big"
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

// publishedDeposit is one deposit event the tracker handed to the publisher, with
// the transaction as it was persisted.
type publishedDeposit struct {
	event types.EventType
	tx    models.Transaction
}

type depositEventLog struct {
	published []publishedDeposit
}

func (l *depositEventLog) Publish(_ context.Context, eventType types.EventType, tx models.Transaction) error {
	l.published = append(l.published, publishedDeposit{event: eventType, tx: tx})
	return nil
}

func (l *depositEventLog) Republish(ctx context.Context, eventType types.EventType, tx models.Transaction, _ time.Time) error {
	return l.Publish(ctx, eventType, tx)
}

func (l *depositEventLog) of(id uuid.UUID) []types.EventType {
	var events []types.EventType
	for _, published := range l.published {
		if published.tx.ID == id {
			events = append(events, published.event)
		}
	}
	return events
}

// finalityChain is a registered mock adapter of chainID whose chain row uses policy.
func finalityChain(t *testing.T, chainID, policy string) (*Service, *mocks.MockChain, *depositEventLog) {
	t.Helper()
	fixtures.TestDB(t)
	adapter := mocks.NewMockChain(chainID)
	adapter.FinalityPolicyVal = policy
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	events := &depositEventLog{}
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetDepositEvents(events)
	return svc, adapter, events
}

// insertRecordedUnder inserts a pending transaction recorded under policy with
// requiredConfs, as the scanner or the executor writes it.
func insertRecordedUnder(t *testing.T, walletID uuid.UUID, chainID, txType, status, policy string, block int64, requiredConfs int) models.Transaction {
	t.Helper()
	tx := fixtures.InsertTransaction(t, walletID, nil, chainID, txType, status, chainID, "1000", block)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", tx.ID).
		Update(map[string]any{"finality": policy, "required_confs": requiredConfs}); err != nil {
		t.Fatalf("record transaction under %s: %v", policy, err)
	}
	tx.Finality, tx.RequiredConfs = policy, requiredConfs
	return tx
}

func requireTransactionState(t *testing.T, id uuid.UUID, status string, confirmations int) models.Transaction {
	t.Helper()
	got := reloadTransaction(t, id)
	if got.Status != status || got.Confirmations != confirmations {
		t.Fatalf("transaction %s: status %s with %d confirmations, want %s with %d", id, got.Status, got.Confirmations, status, confirmations)
	}
	if (status == string(types.TxStatusConfirmed)) != (got.ConfirmedAt != nil) {
		t.Fatalf("transaction %s: status %s with confirmed_at %v", id, got.Status, got.ConfirmedAt)
	}
	return got
}

func TestFinality_FinalizedTag_ConfirmsAtTheFinalizedBlockWithFewerConfirmationsThanRequired(t *testing.T) {
	svc, adapter, events := finalityChain(t, models.ChainTPolygon, models.FinalityFinalizedTag)
	finalized := uint64(99)
	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) { return finalized, nil }
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 100, 128)
	ctx := context.Background()

	// Tip 101: two confirmations, and the finalized block 99 is below the deposit.
	if err := svc.updateConfirmations(ctx, models.ChainTPolygon, adapter, 101); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 2)

	// The finalized block reaches the deposit's: confirmed with 3 of 128 confirmations.
	finalized = 100
	if err := svc.updateConfirmations(ctx, models.ChainTPolygon, adapter, 102); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 3)

	published := events.of(tx.ID)
	if len(published) != 2 || published[0] != types.EventDepositConfirming || published[1] != types.EventDepositConfirmed {
		t.Fatalf("published %v, want confirming then confirmed", published)
	}
	last := events.published[len(events.published)-1].tx
	if last.Finality != models.FinalityFinalizedTag || last.Confirmations != 3 || last.RequiredConfs != 128 {
		t.Fatalf("deposit.confirmed carries finality %q, %d/%d confirmations", last.Finality, last.Confirmations, last.RequiredConfs)
	}
}

func TestFinality_FinalizedTag_NeverConfirmsABlockTheTipHasNotReached(t *testing.T) {
	svc, adapter, _ := finalityChain(t, models.ChainTPolygon, models.FinalityFinalizedTag)
	// A load-balanced node can report a finalized block above the tip another read.
	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) { return 100, nil }
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 100, 3)

	if err := svc.updateConfirmations(context.Background(), models.ChainTPolygon, adapter, 99); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 0)
	if adapter.FinalizedBlockCalls != 0 {
		t.Fatalf("finalized block read %d times for a deposit without a confirmation", adapter.FinalizedBlockCalls)
	}
}

func TestFinality_FinalizedBlockUnavailable_StaysConfirmingUntilTheNextReadSucceeds(t *testing.T) {
	svc, adapter, events := finalityChain(t, models.ChainTBSC, models.FinalityFinalizedTag)
	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) {
		return 0, errors.New("finalized block not found")
	}
	wallet := fixtures.InsertWallet(t, models.ChainTBSC)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTBSC, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 100, 2)
	ctx := context.Background()

	// 101 confirmations, far above the 2 required, and still not confirmed: the count
	// never stands in for a finalized block that could not be read.
	if err := svc.updateConfirmations(ctx, models.ChainTBSC, adapter, 200); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 101)
	if published := events.of(tx.ID); len(published) != 1 || published[0] != types.EventDepositConfirming {
		t.Fatalf("published %v, want deposit.confirming only", published)
	}

	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) { return 199, nil }
	if err := svc.updateConfirmations(ctx, models.ChainTBSC, adapter, 201); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 102)
}

func TestFinality_ConfirmationsPolicy_CountsAsBeforeAndNeverReadsFinality(t *testing.T) {
	svc, adapter, _ := finalityChain(t, models.ChainTPolygon, models.FinalityFinalizedTag)
	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) { return 1000, nil }
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	// Recorded before the chain moved to finalized_tag: it keeps its count.
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityConfirmations, 100, 3)
	ctx := context.Background()

	if err := svc.updateConfirmations(ctx, models.ChainTPolygon, adapter, 101); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 2)
	if err := svc.updateConfirmations(ctx, models.ChainTPolygon, adapter, 102); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 3)
	if adapter.FinalizedBlockCalls != 0 || adapter.SolidifiedBlockCalls != 0 {
		t.Fatalf("finality read %d/%d times for a transaction that counts confirmations", adapter.FinalizedBlockCalls, adapter.SolidifiedBlockCalls)
	}
}

func TestFinality_Solidified_ConfirmsATronDepositAtTheSolidifiedBlock(t *testing.T) {
	svc, adapter, events := finalityChain(t, models.ChainTTron, models.FinalitySolidified)
	solidified := uint64(99)
	adapter.SolidifiedBlockFn = func(context.Context) (uint64, error) { return solidified, nil }
	wallet := fixtures.InsertWallet(t, models.ChainTTron)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTTron, models.TxTypeDeposit, "pending", models.FinalitySolidified, 100, 19)
	ctx := context.Background()

	if err := svc.updateConfirmations(ctx, models.ChainTTron, adapter, 118); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 19)

	solidified = 100
	if err := svc.updateConfirmations(ctx, models.ChainTTron, adapter, 118); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 19)
	if adapter.FinalizedBlockCalls != 0 || adapter.SolidifiedBlockCalls != 2 {
		t.Fatalf("finalized read %d times, solidified %d times", adapter.FinalizedBlockCalls, adapter.SolidifiedBlockCalls)
	}
	if published := events.of(tx.ID); len(published) != 2 || published[1] != types.EventDepositConfirmed {
		t.Fatalf("published %v", published)
	}
}

// countsOnly is an adapter that cannot read any finality.
type countsOnly struct {
	types.Chain
}

func TestFinality_AnAdapterThatCannotReadThePolicyNeverConfirms(t *testing.T) {
	svc, mock, _ := finalityChain(t, models.ChainTPolygon, models.FinalitySolidified)
	adapter := countsOnly{mock}
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalitySolidified, 100, 3)

	if err := svc.updateConfirmations(context.Background(), models.ChainTPolygon, adapter, 500); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 401)
}

func TestFinality_FinalizedTag_ConfirmsAWithdrawalOnceItsBlockIsFinal(t *testing.T) {
	svc, adapter, _ := finalityChain(t, models.ChainTPolygon, models.FinalityFinalizedTag)
	adapter.GetTransactionBlockVal = 100
	adapter.FinalizedBlockFn = func(context.Context) (uint64, error) { return 100, nil }
	withdrawals := &recordingWithdrawalConfirmations{}
	svc.SetWithdrawalConfirmations(withdrawals)
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTPolygon, models.TxTypeWithdrawal, "confirming", models.FinalityFinalizedTag, 0, 3)

	if err := svc.updateConfirmations(context.Background(), models.ChainTPolygon, adapter, 100); err != nil {
		t.Fatal(err)
	}
	got := requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 1)
	if got.BlockNumber != 100 {
		t.Fatalf("block %d, want the reconciled 100", got.BlockNumber)
	}
	if len(withdrawals.confirmed) != 1 || withdrawals.confirmed[0].ID != tx.ID {
		t.Fatalf("withdrawal.confirmed for %+v", withdrawals.confirmed)
	}
}

func TestRun_ConfirmationCheck_ReadsEachChainFinalityOncePerPass(t *testing.T) {
	fixtures.TestDB(t)
	polygon := mocks.NewMockChain(models.ChainTPolygon)
	polygon.FinalityPolicyVal = models.FinalityFinalizedTag
	polygon.GetLatestBlockFn = func(context.Context) (uint64, error) { return 110, nil }
	polygon.FinalizedBlockFn = func(context.Context) (uint64, error) { return 105, nil }
	tron := mocks.NewMockChain(models.ChainTTron)
	tron.FinalityPolicyVal = models.FinalitySolidified
	tron.GetLatestBlockFn = func(context.Context) (uint64, error) { return 300, nil }
	tron.SolidifiedBlockFn = func(context.Context) (uint64, error) { return 0, errors.New("solidity api unreachable") }
	registry := chain.NewRegistry()
	registry.RegisterChain(polygon)
	registry.RegisterChain(tron)
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetDepositEvents(&depositEventLog{})

	polygonWallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	tronWallet := fixtures.InsertWallet(t, models.ChainTTron)
	final := insertRecordedUnder(t, polygonWallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 100, 3)
	atFinalized := insertRecordedUnder(t, polygonWallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 105, 3)
	above := insertRecordedUnder(t, polygonWallet.ID, models.ChainTPolygon, models.TxTypeDeposit, "pending", models.FinalityFinalizedTag, 106, 3)
	tronFirst := insertRecordedUnder(t, tronWallet.ID, models.ChainTTron, models.TxTypeDeposit, "pending", models.FinalitySolidified, 100, 19)
	tronSecond := insertRecordedUnder(t, tronWallet.ID, models.ChainTTron, models.TxTypeDeposit, "pending", models.FinalitySolidified, 101, 19)

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, final.ID, string(types.TxStatusConfirmed), 11)
	requireTransactionState(t, atFinalized.ID, string(types.TxStatusConfirmed), 6)
	requireTransactionState(t, above.ID, string(types.TxStatusConfirming), 5)
	requireTransactionState(t, tronFirst.ID, string(types.TxStatusConfirming), 201)
	requireTransactionState(t, tronSecond.ID, string(types.TxStatusConfirming), 200)
	if polygon.FinalizedBlockCalls != 1 || tron.SolidifiedBlockCalls != 1 {
		t.Fatalf("one pass read the finalized block %d times and the solidified block %d times, want once each", polygon.FinalizedBlockCalls, tron.SolidifiedBlockCalls)
	}

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if polygon.FinalizedBlockCalls != 2 || tron.SolidifiedBlockCalls != 2 {
		t.Fatalf("the next pass read the finalized block %d times and the solidified block %d times in all, want twice each", polygon.FinalizedBlockCalls, tron.SolidifiedBlockCalls)
	}
}

func TestProcess_Transfer_RecordsTheChainFinalityPolicy(t *testing.T) {
	svc, adapter, _ := finalityChain(t, models.ChainTPolygon, models.FinalityFinalizedTag)
	adapter.RequiredConfirmationsVal = 3
	wallet := fixtures.InsertWallet(t, models.ChainTPolygon)
	addr := fixtures.InsertAddress(t, wallet.ID, models.ChainTPolygon, "0xfinality_deposit", "user_finality", 1)

	created, err := svc.processTransfer(context.Background(), models.ChainTPolygon, adapter, types.DetectedTransfer{
		TxHash: "0xfinalitydeposit", BlockNumber: 100, BlockHash: "0xblock", From: "0xsender", To: addr.Address,
		Amount: big.NewInt(1000), Asset: models.ChainTPolygon,
	})
	if err != nil || !created {
		t.Fatalf("processTransfer created=%v err=%v", created, err)
	}
	var tx models.Transaction
	if err := facades.Orm().Query().Where("tx_hash", "0xfinalitydeposit").First(&tx); err != nil {
		t.Fatal(err)
	}
	if tx.Finality != models.FinalityFinalizedTag || tx.RequiredConfs != 3 {
		t.Fatalf("recorded finality %q with %d required confirmations", tx.Finality, tx.RequiredConfs)
	}
}
