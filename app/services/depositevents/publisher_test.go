package depositevents

import (
	"context"
	"errors"
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
	publisher := NewPublisher(enqueuer, walletStore{walletID: {ID: walletID, AccountID: &accountID}}, fixedDecimals{
		"polygon/USDC":  6,
		"eth/ETH":       18,
		"btc/BTC":       8,
		"sol/SOL":       9,
		"bsc/USDT":      18,
		"bsc/bnb":       18,
		"base/eth":      18,
		"arbitrum/USDC": 6,
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

func TestPublish_SendsDecimalAndBaseUnitsForEveryAssetPrecision(t *testing.T) {
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

func TestPublish_RefusesAmbiguousOrForeignInput(t *testing.T) {
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

func TestPublish_WalletWithoutAccountIsNotScopedToAnyAccount(t *testing.T) {
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
