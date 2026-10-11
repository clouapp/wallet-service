package transactions

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/types"
)

type capturedEvent struct{ data any }

func (c *capturedEvent) EnqueueScoped(_ context.Context, event webhook.ScopedEvent) (int, error) {
	c.data = event.Data
	return 1, nil
}

type oneWallet struct{ wallet *models.Wallet }

func (o oneWallet) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) { return o.wallet, nil }

type sixDecimals struct{}

func (sixDecimals) Decimals(string, string) (int, bool) { return 6, true }

// webhookData is the data object deposit.confirmed carries for tx.
func webhookData(t *testing.T, tx models.Transaction) map[string]any {
	t.Helper()
	account := uuid.New()
	captured := &capturedEvent{}
	publisher := depositevents.NewPublisher(depositevents.PublisherDeps{
		Enqueuer: captured,
		Wallets:  oneWallet{wallet: &models.Wallet{ID: tx.WalletID, AccountID: &account}},
		Decimals: sixDecimals{},
	})
	if err := publisher.Publish(context.Background(), types.EventDepositConfirmed, tx); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return decoded(t, captured.data)
}

func decoded(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// A listed deposit carries, under the same names and in the same formats, every
// field the deposit webhook gives a receiver to credit it, so the receiver's
// webhook parser reads both. amount is the exception: base units here, the
// decimal rendering in the webhook (amount_format "decimal").
func TestTransaction_From_CarriesTheDepositWebhookFieldsUnderTheSameNames(t *testing.T) {
	t.Parallel()

	addressID := uuid.New()
	confirmedAt := time.Date(2026, 10, 1, 11, 59, 0, 0, time.UTC)
	tx := models.Transaction{
		ID: uuid.New(), AddressID: &addressID, WalletID: uuid.New(), ExternalUserID: "user-1",
		Chain: "polygon", TxType: models.TxTypeDeposit, TxHash: "0xhash", LogIndex: 4,
		FromAddress: "0xfrom", ToAddress: "0xto", Amount: "001500000", Asset: "USDC",
		TokenContract: "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", Confirmations: 128, RequiredConfs: 128,
		Status: "confirmed", BlockNumber: 777, ConfirmedAt: &confirmedAt, Direction: models.TxDirectionInbound,
	}
	six := 6
	listed := decoded(t, TransactionFrom(withdraw.AccountTransaction{Transaction: tx, Decimals: &six}))
	sent := webhookData(t, tx)

	for _, field := range []string{
		"transaction_id", "wallet_id", "address_id", "external_user_id", "chain", "tx_type", "tx_hash", "log_index",
		"from_address", "to_address", "asset", "token_contract", "amount_base_units", "decimals", "block_number",
		"confirmations", "required_confirmations", "status",
	} {
		got, present := listed[field]
		if !present {
			t.Errorf("%s is missing from the listed transaction", field)
			continue
		}
		if want := sent[field]; got != want {
			t.Errorf("%s = %#v, the webhook sends %#v", field, got, want)
		}
	}
	if listed["amount"] != tx.Amount || sent["amount_format"] != depositevents.AmountFormat {
		t.Errorf("amount = %v (webhook %v as %v), want the stored base units here", listed["amount"], sent["amount"], sent["amount_format"])
	}
	if _, present := listed["amount_format"]; present {
		t.Error("amount_format must stay off the listed transaction: its amount is not the decimal rendering")
	}
}

// A transaction whose amount or asset cannot be stated exactly leaves the
// webhook amount fields out, so a receiver refuses it instead of guessing.
func TestTransaction_From_LeavesOutAnAmountItCannotState(t *testing.T) {
	t.Parallel()

	body := decoded(t, TransactionFrom(withdraw.AccountTransaction{Transaction: models.Transaction{Amount: "1.5", Asset: "unknown"}}))
	for _, field := range []string{"amount_base_units", "decimals"} {
		if value, present := body[field]; present {
			t.Errorf("%s = %#v, want it omitted", field, value)
		}
	}
}

func TestNext_Cursor_EncodesTheNextPositionAndIsNilOnTheLastPage(t *testing.T) {
	t.Parallel()

	if got := NextCursor(nil); got != nil {
		t.Fatalf("NextCursor(nil) = %q, want nil", *got)
	}
	position := withdraw.TransactionPosition{UpdatedAt: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), ID: uuid.New()}
	got := NextCursor(&position)
	if got == nil {
		t.Fatal("NextCursor() = nil, want a cursor")
	}
	at, id, err := pagination.DecodeCursor(*got)
	if err != nil || !at.Equal(position.UpdatedAt) || id != position.ID {
		t.Fatalf("cursor decodes to (%s, %s, %v), want the position", at, id, err)
	}
}
