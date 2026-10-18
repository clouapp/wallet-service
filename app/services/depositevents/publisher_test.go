package depositevents

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

type recordingEnqueuer struct {
	events []webhook.ScopedEvent
	err    error
}

func (r *recordingEnqueuer) EnqueueScoped(_ context.Context, event webhook.ScopedEvent) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	r.events = append(r.events, event)
	return 1, nil
}

type walletStore map[uuid.UUID]*models.Wallet

func (w walletStore) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	return w[id], nil
}

type fixedDecimals map[string]int

func (f fixedDecimals) Decimals(chainID, asset string) (int, bool) {
	decimals, ok := f[chainID+"/"+asset]
	return decimals, ok
}

type fixture struct {
	enqueuer  *recordingEnqueuer
	publisher *Publisher
	accountID uuid.UUID
	walletID  uuid.UUID
}

func newFixture() fixture {
	accountID := uuid.New()
	walletID := uuid.New()
	enqueuer := &recordingEnqueuer{}
	publisher := NewPublisher(PublisherDeps{
		Enqueuer: enqueuer,
		Wallets:  walletStore{walletID: {ID: walletID, AccountID: &accountID}},
		Decimals: fixedDecimals{
			"polygon/USDC":  6,
			"eth/ETH":       18,
			"btc/BTC":       8,
			"sol/SOL":       9,
			"bsc/USDT":      18,
			"bsc/bnb":       18,
			"base/eth":      18,
			"arbitrum/USDC": 6,
			"txrp/xrp":      6,
			"ttron/trx":     6,
		},
	})
	publisher.now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	return fixture{enqueuer: enqueuer, publisher: publisher, accountID: accountID, walletID: walletID}
}

func (f fixture) deposit(chainID, asset, baseUnits string) models.Transaction {
	addressID := uuid.New()
	confirmedAt := time.Date(2026, 10, 1, 11, 59, 0, 0, time.UTC)
	return models.Transaction{
		ID:             uuid.New(),
		AddressID:      &addressID,
		WalletID:       f.walletID,
		ExternalUserID: "user-1",
		Chain:          chainID,
		TxType:         models.TxTypeDeposit,
		TxHash:         "0xdeposit",
		ToAddress:      "0xto",
		FromAddress:    "0xfrom",
		Amount:         baseUnits,
		Asset:          asset,
		Confirmations:  128,
		RequiredConfs:  128,
		Status:         string(types.TxStatusConfirmed),
		BlockNumber:    100,
		ConfirmedAt:    &confirmedAt,
	}
}

func onlyPayload(t *testing.T, enqueuer *recordingEnqueuer) (webhook.ScopedEvent, Payload) {
	t.Helper()
	if len(enqueuer.events) != 1 {
		t.Fatalf("expected one event, got %d", len(enqueuer.events))
	}
	payload, ok := enqueuer.events[0].Data.(Payload)
	if !ok {
		t.Fatalf("event data is %T, want Payload", enqueuer.events[0].Data)
	}
	return enqueuer.events[0], payload
}

func TestPublish_Sends_DecimalAndBaseUnitsForEveryAssetPrecision(t *testing.T) {
	cases := []struct {
		chain, asset, baseUnits string
		decimals                int
		decimal                 string
	}{
		{"polygon", "USDC", "25000000", 6, "25"},
		{"eth", "ETH", "500000000000000000", 18, "0.5"},
		{"btc", "BTC", "150000000", 8, "1.5"},
		{"sol", "SOL", "2500000000", 9, "2.5"},
		{"bsc", "USDT", "12500000000000000000", 18, "12.5"},
		{"bsc", "bnb", "10000000000000000", 18, "0.01"},
		{"base", "eth", "1000000000000000", 18, "0.001"},
		{"arbitrum", "USDC", "3000000", 6, "3"},
	}
	for _, c := range cases {
		t.Run(c.asset, func(t *testing.T) {
			f := newFixture()
			tx := f.deposit(c.chain, c.asset, c.baseUnits)

			if err := f.publisher.Publish(context.Background(), types.EventDepositConfirmed, tx); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			event, payload := onlyPayload(t, f.enqueuer)
			if payload.Amount != c.decimal || payload.AmountBaseUnits != c.baseUnits || payload.Decimals != c.decimals {
				t.Fatalf("amount=%q base=%q decimals=%d, want %q %q %d", payload.Amount, payload.AmountBaseUnits, payload.Decimals, c.decimal, c.baseUnits, c.decimals)
			}
			if payload.AmountFormat != AmountFormat {
				t.Fatalf("amount_format=%q, want %q", payload.AmountFormat, AmountFormat)
			}
			if event.SubjectID != tx.ID.String() || event.WalletID != f.walletID || *event.TransactionID != tx.ID {
				t.Fatalf("unexpected event identity %+v", event)
			}
			if event.AccountID == nil || *event.AccountID != f.accountID {
				t.Fatalf("event must be scoped to the wallet account, got %v", event.AccountID)
			}
			if payload.ToAddress != tx.ToAddress || payload.TxHash != tx.TxHash || payload.Status != tx.Status || payload.ConfirmedAt == nil {
				t.Fatalf("payload lost transaction fields: %+v", payload)
			}
		})
	}
}

func TestPublish_Refuses_AmbiguousOrForeignInput(t *testing.T) {
	f := newFixture()
	valid := f.deposit("polygon", "USDC", "25000000")

	unknownAsset := valid
	unknownAsset.Asset = "DAI"
	humanAmount := valid
	humanAmount.Amount = "25.5"
	negative := valid
	negative.Amount = "-1"
	withdrawal := valid
	withdrawal.TxType = models.TxTypeWithdrawal
	unknownWallet := valid
	unknownWallet.WalletID = uuid.New()

	cases := []struct {
		name      string
		eventType types.EventType
		tx        models.Transaction
		want      error
	}{
		{"unknown decimals", types.EventDepositConfirmed, unknownAsset, ErrUnknownDecimals},
		{"decimal amount stored as base units", types.EventDepositConfirmed, humanAmount, ErrInvalidBaseUnits},
		{"negative amount", types.EventDepositConfirmed, negative, ErrInvalidBaseUnits},
		{"not a deposit", types.EventDepositConfirmed, withdrawal, ErrNotADeposit},
		{"not a deposit event", types.EventWithdrawalConfirmed, valid, ErrUnsupportedEvent},
		{"wallet missing", types.EventDepositConfirmed, unknownWallet, ErrWalletScopeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := f.publisher.Publish(context.Background(), c.eventType, c.tx)
			if !errors.Is(err, c.want) {
				t.Fatalf("Publish error = %v, want %v", err, c.want)
			}
		})
	}
	if len(f.enqueuer.events) != 0 {
		t.Fatalf("nothing may be enqueued for refused input, got %d events", len(f.enqueuer.events))
	}
}

func TestPublish_Wallet_WithoutAccountIsNotScopedToAnyAccount(t *testing.T) {
	f := newFixture()
	orphanWallet := uuid.New()
	f.publisher.wallets = walletStore{orphanWallet: {ID: orphanWallet}}
	tx := f.deposit("polygon", "USDC", "1")
	tx.WalletID = orphanWallet

	if err := f.publisher.Publish(context.Background(), types.EventDepositPending, tx); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	event, payload := onlyPayload(t, f.enqueuer)
	if event.AccountID != nil {
		t.Fatalf("an account-less wallet must not be scoped to an account, got %v", *event.AccountID)
	}
	if payload.Amount != "0.000001" {
		t.Fatalf("amount = %q, want 0.000001", payload.Amount)
	}
}

func TestRepublish_Sends_TheSameEventStampedWithWhenItOccurred(t *testing.T) {
	f := newFixture()
	tx := f.deposit("eth", "ETH", "500000000000000000")
	occurredAt := tx.ConfirmedAt.Add(-time.Second)

	if err := f.publisher.Republish(context.Background(), types.EventDepositConfirmed, tx, occurredAt); err != nil {
		t.Fatalf("Republish: %v", err)
	}

	event, payload := onlyPayload(t, f.enqueuer)
	if !event.OccurredAt.Equal(occurredAt) {
		t.Fatalf("occurred at = %v, want %v", event.OccurredAt, occurredAt)
	}
	if event.SubjectID != tx.ID.String() || event.EventType != types.EventDepositConfirmed {
		t.Fatalf("unexpected event identity %+v", event)
	}
	if payload.Amount != "0.5" || payload.Status != tx.Status || payload.ConfirmedAt == nil {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRepublish_Refuses_AnEventWithoutItsInstant(t *testing.T) {
	f := newFixture()
	err := f.publisher.Republish(context.Background(), types.EventDepositConfirmed, f.deposit("eth", "ETH", "1"), time.Time{})
	if err == nil {
		t.Fatal("a republished event must say when it occurred")
	}
	if len(f.enqueuer.events) != 0 {
		t.Fatalf("nothing may be enqueued, got %d", len(f.enqueuer.events))
	}
}

func TestPublish_Live_EventCarriesNoOccurredAt(t *testing.T) {
	f := newFixture()
	if err := f.publisher.Publish(context.Background(), types.EventDepositConfirmed, f.deposit("eth", "ETH", "1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	event, _ := onlyPayload(t, f.enqueuer)
	if !event.OccurredAt.IsZero() {
		t.Fatalf("a live event reaches every config, got occurred at %v", event.OccurredAt)
	}
}

// payloadJSON is the deposit webhook data object as a receiver decodes it.
func payloadJSON(t *testing.T, payload Payload) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestPublish_Carries_TheXRPDestinationTagOnlyWhenThePaymentHadOne(t *testing.T) {
	tag := uint32(777001)
	tagged := newFixture()
	tx := tagged.deposit("txrp", "xrp", "1000000")
	tx.DestinationTag = &tag
	if err := tagged.publisher.Publish(context.Background(), types.EventDepositPending, tx); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_, payload := onlyPayload(t, tagged.enqueuer)
	if got := payloadJSON(t, payload)["destination_tag"]; got != float64(tag) {
		t.Fatalf("destination_tag = %v, want %d", got, tag)
	}

	for _, deposit := range []struct{ chain, asset, baseUnits string }{
		{"txrp", "xrp", "1000000"},
		{"polygon", "USDC", "25000000"},
	} {
		untagged := newFixture()
		if err := untagged.publisher.Publish(context.Background(), types.EventDepositPending, untagged.deposit(deposit.chain, deposit.asset, deposit.baseUnits)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		_, payload := onlyPayload(t, untagged.enqueuer)
		if got, present := payloadJSON(t, payload)["destination_tag"]; present {
			t.Fatalf("%s deposit without a tag carries destination_tag %v", deposit.chain, got)
		}
	}
}

// finality states the rule that decided status: confirmations (status follows
// confirmations against required_confirmations) or the chain's own finality
// (finalized_tag, solidified), under which required_confirmations is the typical
// finality lag. A row recorded before the field existed reads confirmations.
func TestPublish_Carries_TheFinalityPolicyTheDepositIsConfirmedBy(t *testing.T) {
	for _, tc := range []struct {
		name, chain, asset, recorded, want string
	}{
		{"finalized tag", "polygon", "USDC", models.FinalityFinalizedTag, models.FinalityFinalizedTag},
		{"solidified", "ttron", "trx", models.FinalitySolidified, models.FinalitySolidified},
		{"confirmations", "eth", "ETH", models.FinalityConfirmations, models.FinalityConfirmations},
		{"row without a policy", "btc", "BTC", "", models.FinalityConfirmations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tx := f.deposit(tc.chain, tc.asset, "1")
			tx.Finality = tc.recorded
			if err := f.publisher.Publish(context.Background(), types.EventDepositConfirming, tx); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			_, payload := onlyPayload(t, f.enqueuer)
			decoded := payloadJSON(t, payload)
			if decoded["finality"] != tc.want {
				t.Fatalf("finality = %v, want %s", decoded["finality"], tc.want)
			}
			if decoded["required_confirmations"] != float64(tx.RequiredConfs) {
				t.Fatalf("required_confirmations = %v", decoded["required_confirmations"])
			}
		})
	}
}

// deposit.confirming is progress: each count is its own outbox row, so its subject
// carries the count. The other deposit events stay one per deposit, keyed on the
// transaction id the backfill looks up. The payload keeps the plain transaction id.
func TestPublish_Confirming_SubjectCarriesTheCountAndTheOtherEventsTheTransaction(t *testing.T) {
	f := newFixture()
	tx := f.deposit("eth", "ETH", "1")
	tx.Status = string(types.TxStatusConfirming)
	tx.Confirmations = 7
	tx.ConfirmedAt = nil

	if err := f.publisher.Publish(context.Background(), types.EventDepositConfirming, tx); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	event, payload := onlyPayload(t, f.enqueuer)
	if event.SubjectID != tx.ID.String()+":7" {
		t.Fatalf("deposit.confirming subject = %q, want %s:7", event.SubjectID, tx.ID)
	}
	if *event.TransactionID != tx.ID || payload.TransactionID != tx.ID.String() || payload.ID != tx.ID.String() || payload.Confirmations != 7 {
		t.Fatalf("deposit.confirming identity: transaction %v, payload id %q transaction_id %q, %d confirmations",
			*event.TransactionID, payload.ID, payload.TransactionID, payload.Confirmations)
	}

	for _, eventType := range []types.EventType{types.EventDepositPending, types.EventDepositConfirmed, types.EventDepositFailed} {
		other := newFixture()
		if err := other.publisher.Publish(context.Background(), eventType, other.deposit("eth", "ETH", "1")); err != nil {
			t.Fatalf("Publish %s: %v", eventType, err)
		}
		event, _ := onlyPayload(t, other.enqueuer)
		if event.SubjectID != event.TransactionID.String() {
			t.Fatalf("%s subject = %q, want the transaction id", eventType, event.SubjectID)
		}
	}
}

// webhook_events.subject_id is varchar(64) (migration 260): a uuid, a colon and
// the largest count still fit.
func TestPublish_Confirming_SubjectFitsTheSubjectColumn(t *testing.T) {
	f := newFixture()
	tx := f.deposit("eth", "ETH", "1")
	tx.Confirmations = math.MaxInt

	if err := f.publisher.Publish(context.Background(), types.EventDepositConfirming, tx); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	event, _ := onlyPayload(t, f.enqueuer)
	if len(event.SubjectID) > 64 {
		t.Fatalf("subject %q is %d characters, the column holds 64", event.SubjectID, len(event.SubjectID))
	}
}

func TestPublish_Failed_CarriesTheOnChainFailureCodeAndTheOtherEventsNone(t *testing.T) {
	f := newFixture()
	failed := f.deposit("eth", "ETH", "2000000000000000")
	failed.Status = string(types.TxStatusFailed)
	failed.ErrorMessage = "onchain_out_of_gas"

	if err := f.publisher.Publish(context.Background(), types.EventDepositFailed, failed); err != nil {
		t.Fatal(err)
	}
	event, payload := onlyPayload(t, f.enqueuer)
	if event.EventType != types.EventDepositFailed || event.SubjectID != failed.ID.String() {
		t.Fatalf("deposit.failed is one row per deposit under its transaction id, got %s %s", event.EventType, event.SubjectID)
	}
	if payload.Status != string(types.TxStatusFailed) || payload.FailureCode == nil || *payload.FailureCode != "onchain_out_of_gas" {
		t.Fatalf("payload status %q failure_code %v", payload.Status, payload.FailureCode)
	}
	if payload.ConfirmedAt == nil {
		t.Fatal("a deposit confirmed before its receipt was found failed keeps confirmed_at, so the receiver knows it credited it")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"failure_code":"onchain_out_of_gas"`) {
		t.Fatalf("deposit.failed body %s", raw)
	}

	for _, eventType := range []types.EventType{types.EventDepositPending, types.EventDepositConfirming, types.EventDepositConfirmed} {
		f := newFixture()
		tx := f.deposit("eth", "ETH", "2000000000000000")
		tx.ErrorMessage = "stale text on a row that did not fail"
		if err := f.publisher.Publish(context.Background(), eventType, tx); err != nil {
			t.Fatal(err)
		}
		_, payload := onlyPayload(t, f.enqueuer)
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if payload.FailureCode != nil || strings.Contains(string(raw), "failure_code") {
			t.Fatalf("%s must not carry failure_code: %s", eventType, raw)
		}
	}
}
