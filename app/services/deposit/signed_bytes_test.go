package deposit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// pendingWithdrawal is an EVM withdrawal recorded before its send, with its signed
// bytes sealed for a retry.
func pendingWithdrawal(t *testing.T, walletID models.Wallet) models.Transaction {
	t.Helper()
	tx, _ := evmWithdrawal(t, walletID.ID)
	raw, err := json.Marshal(map[string]any{
		chain.BroadcastOutcomePayloadKey: chain.BroadcastOutcomePending,
		chain.SignedTxPayloadKey:         "enc:v1:sealed-bytes",
		chain.SenderNoncePayloadKey:      41,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", tx.ID).Update("raw_payload", string(raw)); err != nil {
		t.Fatal(err)
	}
	tx.RawPayload = string(raw)
	return tx
}

func TestRunConfirmationCheck_DropsTheSignedBytesOfARowItSettles(t *testing.T) {
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = failedReceiptTip
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	landed := pendingWithdrawal(t, w)
	reverted := pendingWithdrawal(t, w)
	open := pendingWithdrawal(t, w)
	node.Receipts[strings.ToLower(landed.TxHash)] = mocks.FakeEVMReceipt{Block: failedReceiptBlock, Status: "0x1", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice}
	node.Receipts[strings.ToLower(reverted.TxHash)] = mocks.FakeEVMReceipt{Block: failedReceiptBlock, Status: "0x0", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice}

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	if err := svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	for name, row := range map[string]models.Transaction{"mined": landed, "failed on chain": reverted} {
		var payload map[string]any
		got := reloadTransaction(t, row.ID)
		if err := json.Unmarshal([]byte(got.RawPayload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload[chain.SignedTxPayloadKey] != nil || payload[chain.BroadcastOutcomePayloadKey] != chain.BroadcastOutcomeAccepted {
			t.Errorf("%s: raw_payload %v, want the bytes dropped and the send accepted", name, payload)
		}
		if payload[chain.SenderNoncePayloadKey] == nil {
			t.Errorf("%s: raw_payload %v lost its other keys", name, payload)
		}
	}
	if got := reloadTransaction(t, open.ID); !strings.Contains(got.RawPayload, chain.SignedTxPayloadKey) || got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("a row nothing settled keeps its bytes for a retry: %s %s", got.Status, got.RawPayload)
	}
}
