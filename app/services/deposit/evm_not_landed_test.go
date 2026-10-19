package deposit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// notLandedHead is the head of the fake chain these tests read; transfers were sent
// 100 blocks before it.
const notLandedHead = uint64(0xb62520)

func notLandedSender(n int) string { return fmt.Sprintf("0x%040x", 0xbb00+n) }

// sentWithdrawal is an EVM withdrawal from sender recorded under its signed hash
// with the nonce it spends: unknown is a broadcast whose answer did not say whether
// the network took it; head is the chain head read before the send (0: not kept).
func sentWithdrawal(t *testing.T, walletID uuid.UUID, sender string, nonce, head uint64, unknown bool) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx, withdrawal := evmWithdrawal(t, walletID)
	payload := map[string]any{chain.SenderNoncePayloadKey: nonce}
	if head != 0 {
		payload[chain.BroadcastHeadPayloadKey] = head
	}
	if unknown {
		payload[chain.BroadcastOutcomePayloadKey] = chain.BroadcastOutcomeUnknown
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"raw_payload": string(raw), "from_address": sender}); err != nil {
		t.Fatal(err)
	}
	tx.RawPayload, tx.FromAddress = string(raw), sender
	return tx, withdrawal
}

// spendNonce makes block spend nonce of from with the transaction hash, gas limit gas.
func spendNonce(node *mocks.FakeEVMNode, block uint64, from, hash string, nonce uint64, gas uint64) {
	node.SpentNonces = append(node.SpentNonces, mocks.FakeEVMNonceSpend{From: from, Nonce: nonce, Block: block})
	node.Blocks[block] = append(node.Blocks[block], mocks.FakeEVMBlockTx{
		Hash: hash, From: from, To: "0x2222222222222222222222222222222222222222", Value: "0x1",
		Nonce: fmt.Sprintf("0x%x", nonce), Gas: fmt.Sprintf("0x%x", gas),
	})
}

func TestRunConfirmationCheck_EVMUnknownOutcomesAreSettledByThePositiveProofOfTheirNonceSpend(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = fmt.Sprintf("0x%x", notLandedHead)
	node.NoTxIndex = true // publicnode Sepolia: every lookup by hash is null, mined or not
	if node.Blocks == nil {
		node.Blocks = map[uint64][]mocks.FakeEVMBlockTx{}
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/evm", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed),
	})
	sent := notLandedHead - 100
	const otherHash = "0x00000000000000000000000000000000000000000000000000000000000000e1"

	supersededTx, superseded := sentWithdrawal(t, w.ID, notLandedSender(1), 41, sent, true)
	spendNonce(node, notLandedHead-40, notLandedSender(1), otherHash, 41, 21_000)

	landedTx, landed := sentWithdrawal(t, w.ID, notLandedSender(2), 7, sent, true)
	spendNonce(node, notLandedHead-39, notLandedSender(2), landedTx.TxHash, 7, 21_000)
	node.Receipts[strings.ToLower(landedTx.TxHash)] = mocks.FakeEVMReceipt{
		Block: notLandedHead - 39, Status: "0x1", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice,
	}

	revertedTx, reverted := sentWithdrawal(t, w.ID, notLandedSender(3), 9, sent, true)
	spendNonce(node, notLandedHead-38, notLandedSender(3), revertedTx.TxHash, 9, 30_000)
	node.Receipts[strings.ToLower(revertedTx.TxHash)] = mocks.FakeEVMReceipt{
		Block: notLandedHead - 38, Status: "0x0", Gas: 30_000, GasUsed: 25_000, EffectiveGasPrice: failedReceiptGasPrice,
	}

	freeTx, free := sentWithdrawal(t, w.ID, notLandedSender(4), 5, sent, true)

	// A broadcast the node accepted is never failed by this proof, whatever spent its nonce.
	acceptedTx, accepted := sentWithdrawal(t, w.ID, notLandedSender(5), 3, sent, false)
	spendNonce(node, notLandedHead-37, notLandedSender(5), otherHash, 3, 21_000)

	headlessTx, headless := sentWithdrawal(t, w.ID, notLandedSender(6), 2, 0, true)
	spendNonce(node, notLandedHead-36, notLandedSender(6), otherHash, 2, 21_000)

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	for run := 0; run < 2; run++ {
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if got := reloadTransaction(t, supersededTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != 0 || got.ErrorMessage != models.WithdrawalFailureNotLanded {
		t.Fatalf("another transaction spent the nonce: status %s block %d error %q, want failed %s", got.Status, got.BlockNumber, got.ErrorMessage, models.WithdrawalFailureNotLanded)
	}
	if got := reloadWithdrawal(t, superseded.ID); got.Status != models.WithdrawalStatusFailed || got.FailureReason == nil || *got.FailureReason != models.WithdrawalFailureNotLanded {
		t.Fatalf("superseded withdrawal %+v", got)
	}
	if got := reloadTransaction(t, landedTx.ID); got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != int64(notLandedHead-39) {
		t.Fatalf("ours spent the nonce: status %s block %d, want confirmed in block %d", got.Status, got.BlockNumber, notLandedHead-39)
	}
	if got := reloadWithdrawal(t, landed.ID); got.Status != models.WithdrawalStatusConfirmed {
		t.Fatalf("a withdrawal that landed is confirmed, never refunded: %+v", got)
	}
	if got := reloadTransaction(t, revertedTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != int64(notLandedHead-38) || got.ErrorMessage != "onchain_reverted" {
		t.Fatalf("ours was mined and reverted: status %s block %d error %q", got.Status, got.BlockNumber, got.ErrorMessage)
	}
	if got := reloadWithdrawal(t, reverted.ID); got.Status != models.WithdrawalStatusFailed {
		t.Fatalf("reverted withdrawal %+v", got)
	}
	for name, pending := range map[string]struct {
		tx         models.Transaction
		withdrawal models.Withdrawal
	}{
		"its nonce is still free":                 {freeTx, free},
		"its broadcast was accepted":              {acceptedTx, accepted},
		"recorded without the head before a send": {headlessTx, headless},
	} {
		if got := reloadTransaction(t, pending.tx.ID); got.Status != string(types.TxStatusConfirming) || got.ErrorMessage != "" {
			t.Errorf("%s: status %s error %q, want still confirming", name, got.Status, got.ErrorMessage)
		}
		if got := reloadWithdrawal(t, pending.withdrawal.ID); got.Status != models.WithdrawalStatusBroadcast {
			t.Errorf("%s: withdrawal %s, want broadcast", name, got.Status)
		}
		if got := webhookEvents(t, string(types.EventWithdrawalFailed), pending.withdrawal.ID); len(got) != 0 {
			t.Errorf("%s: %d withdrawal.failed events for a transfer that may still land", name, len(got))
		}
	}

	failedEvents := webhookEvents(t, string(types.EventWithdrawalFailed), superseded.ID)
	if len(failedEvents) != 1 {
		t.Fatalf("%d withdrawal.failed events for the superseded withdrawal, want 1", len(failedEvents))
	}
	var body struct {
		Data withdrawalevents.Payload `json:"data"`
	}
	if err := json.Unmarshal([]byte(failedEvents[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.FailureCode == nil || *body.Data.FailureCode != models.WithdrawalFailureNotLanded || body.Data.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal.failed payload %+v", body.Data)
	}
	if got := webhookEvents(t, string(types.EventWithdrawalConfirmed), landed.ID); len(got) != 1 {
		t.Fatalf("%d withdrawal.confirmed events for the landed withdrawal, want 1", len(got))
	}
}

func TestRunConfirmationCheck_EVMUnknownOutcomeStaysPendingWithoutTheStateToProveIt(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = fmt.Sprintf("0x%x", notLandedHead)
	node.NoTxIndex = true
	node.MissingStateBelow = notLandedHead + 1 // no archive: every nonce count read fails
	if node.Blocks == nil {
		node.Blocks = map[uint64][]mocks.FakeEVMBlockTx{}
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	tx, withdrawal := sentWithdrawal(t, w.ID, notLandedSender(1), 41, notLandedHead-100, true)
	spendNonce(node, notLandedHead-40, notLandedSender(1), "0x00000000000000000000000000000000000000000000000000000000000000e1", 41, 21_000)

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("status %s, want confirming while nothing can be proven", got.Status)
	}
	if got := reloadWithdrawal(t, withdrawal.ID); got.Status != models.WithdrawalStatusBroadcast {
		t.Fatalf("withdrawal %s, want broadcast", got.Status)
	}
}

func TestRunConfirmationCheck_EVMUnknownOutcomeWithoutTheStateIsRetriedHourly(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = fmt.Sprintf("0x%x", notLandedHead)
	node.NoTxIndex = true
	node.MissingStateBelow = notLandedHead + 1
	if node.Blocks == nil {
		node.Blocks = map[uint64][]mocks.FakeEVMBlockTx{}
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	tx, _ := sentWithdrawal(t, w.ID, notLandedSender(1), 41, notLandedHead-100, true)
	spendNonce(node, notLandedHead-40, notLandedSender(1), "0x00000000000000000000000000000000000000000000000000000000000000e1", 41, 21_000)
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	svc.now = clock.Now
	counts := func() int { return len(node.CallsTo("eth_getTransactionCount")) }

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := counts()
	var payload map[string]any
	_ = json.Unmarshal([]byte(reloadTransaction(t, tx.ID).RawPayload), &payload)
	if first == 0 || payload[chain.NonceProofRetryAtPayloadKey] != float64(clock.Now().Add(nonceProofRetryAfter).Unix()) {
		t.Fatalf("reads %d, raw_payload %v: the row is backed off for an hour", first, payload)
	}

	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counts() != first {
		t.Fatalf("reads %d after a second pass, want %d: a row known unprovable is not asked again every pass", counts(), first)
	}
	clock.advance(nonceProofRetryAfter + time.Minute)
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if counts() == first {
		t.Fatal("after the back-off the proof is tried again")
	}
}
