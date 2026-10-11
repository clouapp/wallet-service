package deposit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fakexrpl"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// xrpWithdrawal is a broadcast XRP withdrawal: its transaction row (block 0,
// confirming, raw_payload with the LastLedgerSequence) and its withdrawals row.
func xrpWithdrawal(t *testing.T, walletID uuid.UUID, lastLedger uint64, tag *uint32) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx := fixtures.InsertTransaction(t, walletID, nil, models.ChainTXRP, models.TxTypeWithdrawal, "confirming", models.NativeXRP, "3000000", 0)
	raw, err := json.Marshal(map[string]uint64{chain.LastValidBlockPayloadKey: lastLedger})
	if err != nil {
		t.Fatal(err)
	}
	withdrawalID := uuid.New()
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"raw_payload": string(raw), "destination_tag": tag, "idempotency_key": withdrawalID.String()}); err != nil {
		t.Fatal(err)
	}
	withdrawal := models.Withdrawal{
		ID: withdrawalID, WalletID: walletID, TransactionID: &tx.ID, Status: models.WithdrawalStatusBroadcast,
		Amount: "3", DestinationAddress: "rDestination", DestinationTag: tag, FeeEstimate: "0",
	}
	if err := facades.Orm().Query().Create(&withdrawal); err != nil {
		t.Fatal(err)
	}
	return tx, withdrawal
}

func reloadWithdrawal(t *testing.T, id uuid.UUID) models.Withdrawal {
	t.Helper()
	var reloaded models.Withdrawal
	if err := facades.Orm().Query().Where("id = ?", id).FirstOrFail(&reloaded); err != nil {
		t.Fatalf("find withdrawal: %v", err)
	}
	return reloaded
}

func webhookEvents(t *testing.T, eventType string, subject uuid.UUID) []models.WebhookEvent {
	t.Helper()
	var events []models.WebhookEvent
	if err := facades.Orm().Query().Where("event_type = ? AND subject_id = ?", eventType, subject.String()).Find(&events); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRunWithdrawalConfirmationCheck_XRPConfirmsFailsAndExpiresWithdrawalsOnce(t *testing.T) {
	fixtures.TestDB(t)
	node := fakexrpl.New(t) // validated ledger 100, complete ledgers 1-100
	adapter := chainfixtures.NewXRPAltnet(node.URL, func(context.Context, *types.SignedTx) (string, error) {
		t.Fatal("the confirmation tracker broadcast a payment")
		return "", nil
	})
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, models.ChainTXRP)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/xrp", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed),
	})
	tag := uint32(98765)

	succeededTx, succeeded := xrpWithdrawal(t, w.ID, 115, nil)
	failedTx, failed := xrpWithdrawal(t, w.ID, 116, &tag)
	expiredTx, expired := xrpWithdrawal(t, w.ID, 80, nil)
	pendingTx, pending := xrpWithdrawal(t, w.ID, 120, nil)
	node.SetTx(succeededTx.TxHash, fakexrpl.Tx{Validated: true, LedgerIndex: 95, Result: "tesSUCCESS", Fee: "12"})
	node.SetTx(failedTx.TxHash, fakexrpl.Tx{Validated: true, LedgerIndex: 96, Result: "tecNO_DST_INSUF_XRP", Fee: "12"})
	node.SetTx(pendingTx.TxHash, fakexrpl.Tx{Validated: false, Result: "tesSUCCESS", Fee: "12"})

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	for run := 0; run < 2; run++ {
		if err := svc.RunWithdrawalConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	// Validated ledger 100 is the tip, so ledger 95 has 6 confirmations.
	if got := reloadTransaction(t, succeededTx.ID); got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != 95 || got.Confirmations != 6 || got.Fee != "12" {
		t.Fatalf("validated tesSUCCESS: status %s block %d confirmations %d fee %q", got.Status, got.BlockNumber, got.Confirmations, got.Fee)
	}
	if got := reloadWithdrawal(t, succeeded.ID); got.Status != models.WithdrawalStatusConfirmed || got.FailureReason != nil {
		t.Fatalf("confirmed withdrawal %+v", got)
	}
	if got := reloadTransaction(t, succeededTx.ID); got.IdempotencyKey == nil {
		t.Fatal("a confirmed transaction lost its idempotency key")
	}
	// A failed transaction releases the key, so a retry with it records its new transaction.
	for _, id := range []uuid.UUID{failedTx.ID, expiredTx.ID} {
		if got := reloadTransaction(t, id); got.IdempotencyKey != nil {
			t.Fatalf("failed transaction %s still holds idempotency key %s", id, *got.IdempotencyKey)
		}
	}

	const tecCode = "onchain_tecno_dst_insuf_xrp"
	if got := reloadTransaction(t, failedTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != 96 || got.Fee != "12" || got.ErrorMessage != tecCode {
		t.Fatalf("validated tec: status %s block %d fee %q error %q", got.Status, got.BlockNumber, got.Fee, got.ErrorMessage)
	}
	if got := reloadWithdrawal(t, failed.ID); got.Status != models.WithdrawalStatusFailed || got.FailureReason == nil || *got.FailureReason != tecCode {
		t.Fatalf("tec withdrawal %+v", got)
	}

	if got := reloadTransaction(t, expiredTx.ID); got.Status != string(types.TxStatusFailed) || got.BlockNumber != 0 || got.Fee != "" || got.ErrorMessage != models.WithdrawalFailureExpired {
		t.Fatalf("expired: status %s block %d fee %q error %q", got.Status, got.BlockNumber, got.Fee, got.ErrorMessage)
	}
	if got := reloadWithdrawal(t, expired.ID); got.Status != models.WithdrawalStatusFailed || got.FailureReason == nil || *got.FailureReason != models.WithdrawalFailureExpired {
		t.Fatalf("expired withdrawal %+v", got)
	}

	if got := reloadTransaction(t, pendingTx.ID); got.Status != "confirming" || got.Fee != "" {
		t.Fatalf("not validated yet: status %s fee %q", got.Status, got.Fee)
	}
	if got := reloadWithdrawal(t, pending.ID); got.Status != models.WithdrawalStatusBroadcast {
		t.Fatalf("pending withdrawal %+v", got)
	}

	// Two tracker runs, one event per withdrawal and outcome.
	for name, want := range map[string]struct {
		event   types.EventType
		subject uuid.UUID
		count   int
	}{
		"confirmed": {types.EventWithdrawalConfirmed, succeeded.ID, 1},
		"tec":       {types.EventWithdrawalFailed, failed.ID, 1},
		"expired":   {types.EventWithdrawalFailed, expired.ID, 1},
		"pending":   {types.EventWithdrawalFailed, pending.ID, 0},
	} {
		if got := webhookEvents(t, string(want.event), want.subject); len(got) != want.count {
			t.Fatalf("%s: %d %s events, want %d", name, len(got), want.event, want.count)
		}
	}
	var body struct {
		Data withdrawalevents.Payload `json:"data"`
	}
	if err := json.Unmarshal([]byte(webhookEvents(t, string(types.EventWithdrawalFailed), failed.ID)[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.FailureCode == nil || *body.Data.FailureCode != tecCode || body.Data.TxHash == nil || *body.Data.TxHash != failedTx.TxHash ||
		body.Data.DestinationTag == nil || *body.Data.DestinationTag != tag || body.Data.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal.failed payload %+v", body.Data)
	}
}

func TestOnChainFailureCode_IsAPublicMachineCode(t *testing.T) {
	for result, want := range map[string]string{
		"tecNO_DST_INSUF_XRP": "onchain_tecno_dst_insuf_xrp",
		"tecUNFUNDED_PAYMENT": "onchain_tecunfunded_payment",
		"":                    "onchain_failed",
		"tec-bad result":      "onchain_failed",
		"tecTHIS_RESULT_CODE_IS_FAR_TOO_LONG_FOR_THE_FAILURE_REASON_COLUMN": "onchain_failed",
	} {
		if got := onChainFailureCode(result); got != want {
			t.Errorf("%q = %q, want %q", result, got, want)
		}
	}
}
