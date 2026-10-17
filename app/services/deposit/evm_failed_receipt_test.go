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

// Sepolia, 2026-10-18: the withdrawal built with the fixed 21000 limit was mined
// in block 0xb62506 with status 0x0, gasUsed 21000 of 21000, at 0x399c6 wei/gas.
const (
	failedReceiptBlock    = uint64(0xb62506)
	failedReceiptGasPrice = uint64(0x399c6)
	failedReceiptTip      = "0xb62520" // 27 blocks: past every required confirmation
)

// evmWithdrawal is a broadcast EVM withdrawal: its transaction row (block 0,
// confirming) and its withdrawals row.
func evmWithdrawal(t *testing.T, walletID uuid.UUID) (models.Transaction, models.Withdrawal) {
	t.Helper()
	tx := fixtures.InsertTransaction(t, walletID, nil, models.ChainTETH, models.TxTypeWithdrawal, "confirming", models.NativeETH, "2000000000000000", 0)
	withdrawalID := uuid.New()
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).
		Update(map[string]any{"idempotency_key": withdrawalID.String()}); err != nil {
		t.Fatal(err)
	}
	withdrawal := models.Withdrawal{
		ID: withdrawalID, WalletID: walletID, TransactionID: &tx.ID, Status: models.WithdrawalStatusBroadcast,
		Amount: "0.002", DestinationAddress: "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a", FeeEstimate: "0",
	}
	if err := facades.Orm().Query().Create(&withdrawal); err != nil {
		t.Fatal(err)
	}
	return tx, withdrawal
}

func eventsOfType(t *testing.T, eventType types.EventType) []models.WebhookEvent {
	t.Helper()
	var events []models.WebhookEvent
	if err := facades.Orm().Query().Where("event_type = ?", string(eventType)).Find(&events); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRunConfirmationCheck_EVMFailedReceiptsFailTheTransactionAndItsWithdrawal(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = failedReceiptTip
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/evm", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed), string(types.EventSweepConfirmed),
	})

	outOfGasTx, outOfGas := evmWithdrawal(t, w.ID)
	succeededTx, succeeded := evmWithdrawal(t, w.ID)
	revertedSweep := fixtures.InsertTransaction(t, w.ID, nil, models.ChainTETH, models.TxTypeSweep, "confirming", models.NativeETH, "1000", 0)
	outOfGasSeed := fixtures.InsertTransaction(t, w.ID, nil, models.ChainTETH, models.TxTypeGasSeed, "confirming", models.NativeETH, "300", 0)
	for hash, receipt := range map[string]mocks.FakeEVMReceipt{
		outOfGasTx.TxHash:    {Block: failedReceiptBlock, Status: "0x0", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice},
		succeededTx.TxHash:   {Block: failedReceiptBlock, Status: "0x1", Gas: 25_200, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice},
		revertedSweep.TxHash: {Block: failedReceiptBlock, Status: "0x0", Gas: 100_000, GasUsed: 31_000, EffectiveGasPrice: failedReceiptGasPrice},
		outOfGasSeed.TxHash:  {Block: failedReceiptBlock, Status: "0x0", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice},
	} {
		node.Receipts[strings.ToLower(hash)] = receipt
	}

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	for run := 0; run < 2; run++ {
		if err := svc.RunConfirmationCheck(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	const outOfGasCode, revertedCode = "onchain_out_of_gas", "onchain_reverted"
	paidAll := "4955454000" // 21000 × 0x399c6: the fee was paid, the value was not moved
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		fee  string
		code string
	}{
		{"out-of-gas withdrawal", outOfGasTx.ID, paidAll, outOfGasCode},
		{"reverted sweep", revertedSweep.ID, "7315194000", revertedCode},
		{"out-of-gas gas_seed", outOfGasSeed.ID, paidAll, outOfGasCode},
	} {
		got := reloadTransaction(t, tc.id)
		if got.Status != string(types.TxStatusFailed) || got.BlockNumber != int64(failedReceiptBlock) || got.Fee != tc.fee || got.ErrorMessage != tc.code {
			t.Errorf("%s: status %s block %d fee %q error %q, want failed in block %d, fee %s, %s",
				tc.name, got.Status, got.BlockNumber, got.Fee, got.ErrorMessage, failedReceiptBlock, tc.fee, tc.code)
		}
	}
	if got := reloadWithdrawal(t, outOfGas.ID); got.Status != models.WithdrawalStatusFailed || got.FailureReason == nil || *got.FailureReason != outOfGasCode {
		t.Fatalf("out-of-gas withdrawal %+v", got)
	}
	if got := reloadTransaction(t, succeededTx.ID); got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != int64(failedReceiptBlock) {
		t.Fatalf("status 0x1: status %s block %d, want confirmed", got.Status, got.BlockNumber)
	}
	if got := reloadWithdrawal(t, succeeded.ID); got.Status != models.WithdrawalStatusConfirmed {
		t.Fatalf("confirmed withdrawal %+v", got)
	}

	// Two tracker runs, one event per withdrawal and outcome; a failed sweep is never confirmed.
	for name, want := range map[string]struct {
		event   types.EventType
		subject uuid.UUID
		count   int
	}{
		"failed":                  {types.EventWithdrawalFailed, outOfGas.ID, 1},
		"failed is not confirmed": {types.EventWithdrawalConfirmed, outOfGas.ID, 0},
		"confirmed":               {types.EventWithdrawalConfirmed, succeeded.ID, 1},
		"confirmed is not failed": {types.EventWithdrawalFailed, succeeded.ID, 0},
	} {
		if got := webhookEvents(t, string(want.event), want.subject); len(got) != want.count {
			t.Fatalf("%s: %d %s events, want %d", name, len(got), want.event, want.count)
		}
	}
	if got := eventsOfType(t, types.EventSweepConfirmed); len(got) != 0 {
		t.Fatalf("a reverted sweep sent %d sweep.confirmed events", len(got))
	}
	var body struct {
		Data withdrawalevents.Payload `json:"data"`
	}
	if err := json.Unmarshal([]byte(webhookEvents(t, string(types.EventWithdrawalFailed), outOfGas.ID)[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.FailureCode == nil || *body.Data.FailureCode != outOfGasCode || body.Data.TxHash == nil ||
		*body.Data.TxHash != outOfGasTx.TxHash || body.Data.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal.failed payload %+v", body.Data)
	}
}
