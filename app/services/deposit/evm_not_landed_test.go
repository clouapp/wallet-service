package deposit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

const notLandedSender = "0x2f5c1b4e4b5d0f3c8a9e7d6c5b4a39281706f5e4"

// unknownOutcomeWithdrawal is an EVM withdrawal whose broadcast answer did not say
// whether the network took it: recorded under its signed hash with the nonce it
// spends. A nil nonce is a row recorded before the nonce was kept.
func unknownOutcomeWithdrawal(t *testing.T, walletID uuid.UUID, nonce *uint64) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx, withdrawal := evmWithdrawal(t, walletID)
	payload := map[string]any{chain.BroadcastOutcomePayloadKey: chain.BroadcastOutcomeUnknown}
	if nonce != nil {
		payload[chain.SenderNoncePayloadKey] = *nonce
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"raw_payload": string(raw), "from_address": notLandedSender}); err != nil {
		t.Fatal(err)
	}
	tx.RawPayload, tx.FromAddress = string(raw), notLandedSender
	return tx, withdrawal
}

func TestRunConfirmationCheck_EVMUnknownOutcomesSettleByHashAndFailOnlyWhenTheirNonceWasSpent(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = failedReceiptTip
	node.NonceHex = "0x2a" // the sender's mined nonce count is 42: nonces up to 41 are spent
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/evm", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed),
	})
	spent, free := uint64(41), uint64(42)

	supersededTx, superseded := unknownOutcomeWithdrawal(t, w.ID, &spent)
	landedTx, landed := unknownOutcomeWithdrawal(t, w.ID, &spent)
	inMempoolTx, inMempool := unknownOutcomeWithdrawal(t, w.ID, &spent)
	freeNonceTx, freeNonce := unknownOutcomeWithdrawal(t, w.ID, &free)
	noNonceTx, noNonce := unknownOutcomeWithdrawal(t, w.ID, nil)
	node.Receipts[strings.ToLower(landedTx.TxHash)] = mocks.FakeEVMReceipt{
		Block: failedReceiptBlock, Status: "0x1", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice,
	}
	// Known to the node, not mined yet (eth_getTransactionByHash answers it, no receipt).
	node.Receipts[strings.ToLower(inMempoolTx.TxHash)] = mocks.FakeEVMReceipt{Gas: 21_000, EffectiveGasPrice: failedReceiptGasPrice}

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	for run := 0; run < 2; run++ {
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if got := reloadTransaction(t, supersededTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != 0 || got.Fee != "" ||
		got.ErrorMessage != models.WithdrawalFailureNotLanded {
		t.Fatalf("superseded: status %s block %d fee %q error %q, want failed %s with no block or fee",
			got.Status, got.BlockNumber, got.Fee, got.ErrorMessage, models.WithdrawalFailureNotLanded)
	}
	if got := reloadWithdrawal(t, superseded.ID); got.Status != models.WithdrawalStatusFailed || got.FailureReason == nil ||
		*got.FailureReason != models.WithdrawalFailureNotLanded {
		t.Fatalf("superseded withdrawal %+v", got)
	}
	if got := reloadTransaction(t, landedTx.ID); got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != int64(failedReceiptBlock) {
		t.Fatalf("an unknown outcome that was mined: status %s block %d, want confirmed", got.Status, got.BlockNumber)
	}
	if got := reloadWithdrawal(t, landed.ID); got.Status != models.WithdrawalStatusConfirmed {
		t.Fatalf("mined withdrawal %+v", got)
	}
	for name, pending := range map[string]struct {
		tx         models.Transaction
		withdrawal models.Withdrawal
	}{
		"known to the node":                 {inMempoolTx, inMempool},
		"its nonce is still free":           {freeNonceTx, freeNonce},
		"recorded without the sender nonce": {noNonceTx, noNonce},
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

	// Two tracker runs, one withdrawal.failed for the transfer that can never land.
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
	if body.Data.FailureCode == nil || *body.Data.FailureCode != models.WithdrawalFailureNotLanded || body.Data.TxHash == nil ||
		*body.Data.TxHash != supersededTx.TxHash || body.Data.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal.failed payload %+v", body.Data)
	}
}

func TestRunConfirmationCheck_EVMUnknownOutcomeStaysPendingWhileTheNodeCannotAnswer(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = failedReceiptTip
	node.NonceHex = "not-a-quantity"
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	spent := uint64(41)
	tx, withdrawal := unknownOutcomeWithdrawal(t, w.ID, &spent)

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("status %s, want confirming while the nonce cannot be read", got.Status)
	}
	if got := reloadWithdrawal(t, withdrawal.ID); got.Status != models.WithdrawalStatusBroadcast {
		t.Fatalf("withdrawal %s, want broadcast", got.Status)
	}
}
