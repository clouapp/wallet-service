package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// Sepolia, 2026-10-17: 0.002 ETH sent with 21000 gas to the fresh deposit address
// 0xa9c37e6b…f80a was mined in block 0xb62506 with status 0x0 (out of gas). The
// value never moved, yet the scanner recorded and confirmed it as a deposit.
const (
	sepoliaFailedTransferHash = "0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb"
	sepoliaDepositAddress     = "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a"
	sepoliaSender             = "0x6ea8ac1673402989e7b653ae4e83b54173719c30"
	succeededDepositHash      = "0x00000000000000000000000000000000000000000000000000000000000000d1"
	unwatchedTransferHash     = "0x00000000000000000000000000000000000000000000000000000000000000d2"
	succeededDepositAddress   = "0x00000000000000000000000000000000000000b0"
	unwatchedAddress          = "0x00000000000000000000000000000000000000c0"
	failedTransferBaseUnits   = "2000000000000000"
	outOfGasCode              = "onchain_out_of_gas"
)

var failedTransferValueHex = "0x" + mustBaseUnits(failedTransferBaseUnits).Text(16)

func mustBaseUnits(value string) *big.Int {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("bad base units " + value)
	}
	return parsed
}

// failedDepositFixture is the teth adapter against a fake Sepolia node, a wallet
// with two watched deposit addresses, and the deposit webhooks recorded in the
// outbox of a config that subscribes to every deposit event.
type failedDepositFixture struct {
	node   *mocks.FakeEVMNode
	svc    *Service
	wallet models.Wallet
}

func newFailedDepositFixture(t *testing.T) failedDepositFixture {
	t.Helper()
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = failedReceiptTip
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))

	account := fixtures.InsertAccount(t, "failed deposit owner")
	wallet := fixtures.InsertWalletWithAccount(t, models.ChainTETH, &account.ID)
	fixtures.InsertAddress(t, wallet.ID, models.ChainTETH, sepoliaDepositAddress, "player-failed", 1)
	fixtures.InsertAddress(t, wallet.ID, models.ChainTETH, succeededDepositAddress, "player-succeeded", 2)
	fixtures.InsertScopedWebhookConfig(t, "https://hooks.example.test/deposits", depositHookSecret, []string{
		string(types.EventDepositPending), string(types.EventDepositConfirming),
		string(types.EventDepositConfirmed), string(types.EventDepositFailed),
	}, &account.ID, nil)

	webhookSvc := newWebhookSvc()
	svc := newDepositSvc(registry, webhookSvc)
	svc.SetDepositEvents(newDepositPublisher(webhookSvc, chainAssetDecimals{"teth/eth": etherDecimals, "teth/usdc": 6}))
	return failedDepositFixture{node: node, svc: svc, wallet: wallet}
}

// failedTransferBlock lists block 0xb62506 with the failed transfer to the watched
// address, a transfer that succeeded to the other watched address, a transfer to
// an address nobody watches, and a zero-value call to the watched address.
func (f failedDepositFixture) failedTransferBlock() {
	f.node.Blocks[failedReceiptBlock] = []mocks.FakeEVMBlockTx{
		{Hash: sepoliaFailedTransferHash, From: sepoliaSender, To: sepoliaDepositAddress, Value: failedTransferValueHex},
		{Hash: succeededDepositHash, From: sepoliaSender, To: succeededDepositAddress, Value: "0x5af3107a4000"},
		{Hash: unwatchedTransferHash, From: sepoliaSender, To: unwatchedAddress, Value: "0x1"},
		{Hash: "0x00000000000000000000000000000000000000000000000000000000000000d3", From: sepoliaSender, To: sepoliaDepositAddress, Value: "0x0"},
	}
	f.receipt(sepoliaFailedTransferHash, "0x0", 21_000)
	f.receipt(succeededDepositHash, "0x1", 21_000)
}

func (f failedDepositFixture) receipt(hash, status string, gasUsed uint64) {
	f.node.Receipts[strings.ToLower(hash)] = mocks.FakeEVMReceipt{
		Block: failedReceiptBlock, Status: status, Gas: 21_000, GasUsed: gasUsed, EffectiveGasPrice: failedReceiptGasPrice,
	}
}

func depositRows(t *testing.T) map[string]models.Transaction {
	t.Helper()
	var rows []models.Transaction
	if err := facades.Orm().Query().Where("tx_type = ?", models.TxTypeDeposit).Find(&rows); err != nil {
		t.Fatal(err)
	}
	byHash := make(map[string]models.Transaction, len(rows))
	for _, row := range rows {
		byHash[strings.ToLower(row.TxHash)] = row
	}
	return byHash
}

func receiptCallsFor(node *mocks.FakeEVMNode, hash string) int {
	count := 0
	for _, call := range node.CallsTo("eth_getTransactionReceipt") {
		if len(call.Params) > 0 && strings.Contains(strings.ToLower(string(call.Params[0])), strings.ToLower(hash)) {
			count++
		}
	}
	return count
}

func TestScan_Block_EVMNativeTransferWhoseReceiptFailedIsNotADeposit(t *testing.T) {
	f := newFailedDepositFixture(t)
	f.failedTransferBlock()

	recorded, err := f.svc.ScanBlock(context.Background(), models.ChainTETH, failedReceiptBlock)

	if err != nil || recorded != 1 {
		t.Fatalf("recorded %d err %v, want the one transfer whose receipt succeeded", recorded, err)
	}
	rows := depositRows(t)
	if _, credited := rows[sepoliaFailedTransferHash]; credited || len(rows) != 1 {
		t.Fatalf("a transfer mined with status 0x0 moved no value and must not be a deposit, got rows %v", rows)
	}
	if got := rows[succeededDepositHash]; got.ToAddress != succeededDepositAddress || got.Amount != "100000000000000" || got.Status != string(types.TxStatusPending) {
		t.Fatalf("succeeded deposit %+v", got)
	}
	if pendings := eventsOfType(t, types.EventDepositPending); len(pendings) != 1 {
		t.Fatalf("%d deposit.pending events, want only the deposit that moved its value", len(pendings))
	}
	if blocks := len(f.node.CallsTo("eth_getBlockReceipts")); blocks != 1 || len(f.node.CallsTo("eth_getTransactionReceipt")) != 0 {
		t.Fatalf("two candidates of one block take one eth_getBlockReceipts: %d, and %d eth_getTransactionReceipt", blocks, len(f.node.CallsTo("eth_getTransactionReceipt")))
	}
}

func TestScan_Block_EVMReadsReceiptsOnlyForTransfersToWatchedAddresses(t *testing.T) {
	f := newFailedDepositFixture(t)
	f.node.Blocks[failedReceiptBlock] = []mocks.FakeEVMBlockTx{
		{Hash: unwatchedTransferHash, From: sepoliaSender, To: unwatchedAddress, Value: "0x1"},
	}

	recorded, err := f.svc.ScanBlock(context.Background(), models.ChainTETH, failedReceiptBlock)

	if err != nil || recorded != 0 {
		t.Fatalf("recorded %d err %v", recorded, err)
	}
	if receipts := len(f.node.CallsTo("eth_getTransactionReceipt")) + len(f.node.CallsTo("eth_getBlockReceipts")); receipts != 0 {
		t.Fatalf("a block without a transfer to a watched address reads no receipt, got %d reads", receipts)
	}

	f.node.Blocks[failedReceiptBlock] = append(f.node.Blocks[failedReceiptBlock],
		mocks.FakeEVMBlockTx{Hash: sepoliaFailedTransferHash, From: sepoliaSender, To: sepoliaDepositAddress, Value: failedTransferValueHex})
	f.receipt(sepoliaFailedTransferHash, "0x0", 21_000)
	if _, err := f.svc.ScanBlock(context.Background(), models.ChainTETH, failedReceiptBlock); err != nil {
		t.Fatal(err)
	}
	if got := receiptCallsFor(f.node, sepoliaFailedTransferHash); got != 1 || len(f.node.CallsTo("eth_getBlockReceipts")) != 0 {
		t.Fatalf("one candidate reads its own receipt once (%d), not the block's", got)
	}
	if rows := depositRows(t); len(rows) != 0 {
		t.Fatalf("rows %v", rows)
	}
}

func TestScan_Block_EVMBlockWhoseReceiptsCannotBeReadGoesPendingAndRecordsOnRetry(t *testing.T) {
	f := newFailedDepositFixture(t)
	f.failedTransferBlock()
	store, storeErr := pending.NewFileStore(pending.FileStoreDeps{Dir: t.TempDir()})
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	f.svc.SetPendingStore(store)
	if policyErr := f.svc.SetFailurePolicy(FailurePolicy{
		ImmediateRetries: 1, ImmediateRetryDelay: time.Millisecond,
		PendingRetryDelay: time.Second, PendingRetryMaxDelay: time.Second, MaxNewPendingPerCycle: 1,
	}); policyErr != nil {
		t.Fatal(policyErr)
	}
	f.node.ReceiptsHTTPStatus = 503

	_, scanErr := f.svc.ScanBlock(context.Background(), models.ChainTETH, failedReceiptBlock)

	if scanErr == nil || !strings.Contains(scanErr.Error(), "saved as pending") || !errors.Is(scanErr, chain.ErrProviderUnavailable) {
		t.Fatalf("a block whose receipts did not load must fail and be kept for retry, got %v", scanErr)
	}
	entries, err := store.List(context.Background(), models.ChainTETH)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Block != failedReceiptBlock || entries[0].ErrorClass != pending.ClassRPC {
		t.Fatalf("pending entries %+v, want block %d with an rpc error", entries, failedReceiptBlock)
	}
	failedTx := strings.Join(entries[0].TxHashes, ",")
	if !strings.Contains(failedTx, sepoliaFailedTransferHash) || !strings.Contains(failedTx, succeededDepositHash) {
		t.Fatalf("the pending entry must name the transfers whose receipts were not read, got %q", failedTx)
	}
	if rows := depositRows(t); len(rows) != 0 {
		t.Fatalf("a transfer whose receipt was not read is neither credited nor dropped for good, got rows %v", rows)
	}

	f.node.ReceiptsHTTPStatus = 0
	result, err := f.svc.ReprocessPending(context.Background(), models.ChainTETH, true)
	if err != nil || result.Resolved != 1 {
		t.Fatalf("retry resolved %d err %v", result.Resolved, err)
	}
	rows := depositRows(t)
	if _, credited := rows[sepoliaFailedTransferHash]; credited || len(rows) != 1 || rows[succeededDepositHash].ID == uuid.Nil {
		t.Fatalf("the retry records only the deposit that moved its value, got rows %v", rows)
	}
}

// insertNativeDeposit is a deposit row the scanner (or the ingest) recorded in
// block 0xb62506 before receipts were read.
func (f failedDepositFixture) insertNativeDeposit(t *testing.T, status, tokenContract string) models.Transaction {
	t.Helper()
	asset := models.NativeETH
	if tokenContract != "" {
		asset = "usdc"
	}
	tx := fixtures.InsertTransaction(t, f.wallet.ID, nil, models.ChainTETH, models.TxTypeDeposit, status, asset, failedTransferBaseUnits, int64(failedReceiptBlock))
	columns := map[string]any{"to_address": sepoliaDepositAddress, "token_contract": tokenContract}
	if status == string(types.TxStatusConfirmed) {
		columns["confirmed_at"] = time.Now().UTC().Add(-time.Hour)
		columns["confirmations"] = 19
	}
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).Update(columns); err != nil {
		t.Fatal(err)
	}
	return reloadTransaction(t, tx.ID)
}

func depositEventsFor(t *testing.T, eventType types.EventType, subject uuid.UUID) []depositevents.Payload {
	t.Helper()
	rows := webhookEvents(t, string(eventType), subject)
	payloads := make([]depositevents.Payload, 0, len(rows))
	for _, row := range rows {
		var body struct {
			Data depositevents.Payload `json:"data"`
		}
		if err := json.Unmarshal([]byte(row.Payload), &body); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, body.Data)
	}
	return payloads
}

func TestRunConfirmationCheck_EVMNativeDepositWhoseReceiptFailedIsFailedInsteadOfConfirmed(t *testing.T) {
	f := newFailedDepositFixture(t)
	failed := f.insertNativeDeposit(t, string(types.TxStatusConfirming), "")
	succeeded := f.insertNativeDeposit(t, string(types.TxStatusConfirming), "")
	token := f.insertNativeDeposit(t, string(types.TxStatusConfirming), "0x1c7d4b196cb0c7b01d743fbc6116a902379c7238")
	unread := f.insertNativeDeposit(t, string(types.TxStatusConfirming), "")
	f.receipt(failed.TxHash, "0x0", 21_000)
	f.receipt(succeeded.TxHash, "0x1", 21_000)

	for pass := 0; pass < 2; pass++ {
		if err := f.svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if got := reloadTransaction(t, failed.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != outOfGasCode || got.ConfirmedAt != nil {
		t.Fatalf("failed receipt: status %s error %q confirmed_at %v, want failed with %s and never confirmed", got.Status, got.ErrorMessage, got.ConfirmedAt, outOfGasCode)
	}
	failedEvents := depositEventsFor(t, types.EventDepositFailed, failed.ID)
	if len(failedEvents) != 1 || len(depositEventsFor(t, types.EventDepositConfirmed, failed.ID)) != 0 {
		t.Fatalf("two passes: %d deposit.failed and %d deposit.confirmed, want exactly one deposit.failed", len(failedEvents), len(depositEventsFor(t, types.EventDepositConfirmed, failed.ID)))
	}
	if body := failedEvents[0]; body.Status != string(types.TxStatusFailed) || body.FailureCode == nil || *body.FailureCode != outOfGasCode ||
		body.TxHash != failed.TxHash || body.AmountBaseUnits != failedTransferBaseUnits || body.ConfirmedAt != nil {
		t.Fatalf("deposit.failed payload %+v", body)
	}

	for name, tx := range map[string]models.Transaction{"succeeded receipt": succeeded, "token transfer": token} {
		if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusConfirmed) {
			t.Fatalf("%s: status %s, want confirmed", name, got.Status)
		}
		if len(depositEventsFor(t, types.EventDepositConfirmed, tx.ID)) != 1 || len(depositEventsFor(t, types.EventDepositFailed, tx.ID)) != 0 {
			t.Fatalf("%s: want one deposit.confirmed and no deposit.failed", name)
		}
	}
	if receiptCallsFor(f.node, token.TxHash) != 0 {
		t.Fatal("an ERC-20 deposit comes from a Transfer log, which a failed transaction never emits: its receipt is not read")
	}

	if got := reloadTransaction(t, unread.ID); got.Status != string(types.TxStatusConfirming) || got.ConfirmedAt != nil {
		t.Fatalf("a native deposit whose receipt is not found stays unconfirmed, got %s", got.Status)
	}
	if len(depositEventsFor(t, types.EventDepositConfirmed, unread.ID))+len(depositEventsFor(t, types.EventDepositFailed, unread.ID)) != 0 {
		t.Fatal("a deposit whose receipt was not read is neither confirmed nor failed")
	}
	if got := receiptCallsFor(f.node, unread.TxHash); got != 2 {
		t.Fatalf("an unread receipt is read again on the next pass, got %d reads in two passes", got)
	}
}
