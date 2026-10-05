package deposit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	depositConfirmedEvent = "deposit.confirmed"
	depositHookSecret     = "deposit-events-secret"
	halfEtherBaseUnits    = "500000000000000000"
	etherDecimals         = 18
	depositBlock          = 100
	confirmedAtBlock      = 103
)

type chainAssetDecimals map[string]int

func (d chainAssetDecimals) Decimals(chainID, asset string) (int, bool) {
	decimals, ok := d[chainID+"/"+asset]
	return decimals, ok
}

type depositEventsFixture struct {
	svc       *Service
	adapter   *mocks.MockChain
	publisher *depositevents.Publisher
	wallet    models.Wallet
	accountID uuid.UUID
}

func newDepositEventsFixture(t *testing.T) depositEventsFixture {
	t.Helper()
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	adapter := mocks.NewMockChain("eth")
	adapter.RequiredConfirmationsVal = confirmedAtBlock - depositBlock
	registry.RegisterChain(adapter)

	account := mocks.InsertAccount(t, "deposit owner")
	wallet := mocks.InsertWalletWithAccount(t, "eth", &account.ID)

	webhookSvc := newWebhookSvc()
	publisher := depositevents.NewPublisher(depositevents.PublisherDeps{
		Enqueuer: webhookSvc,
		Wallets:  repositories.NewWalletRepository(nil),
		Decimals: chainAssetDecimals{"eth/eth": etherDecimals},
	})
	svc := newDepositSvc(registry, webhookSvc)
	svc.SetDepositEvents(publisher)
	return depositEventsFixture{svc: svc, adapter: adapter, publisher: publisher, wallet: wallet, accountID: account.ID}
}

func depositWebhookRows(t *testing.T) []models.WebhookEvent {
	t.Helper()
	var events []models.WebhookEvent
	if err := facades.Orm().Query().Where("event_type = ?", depositConfirmedEvent).Find(&events); err != nil {
		t.Fatalf("load webhook events: %v", err)
	}
	return events
}

func TestUpdateConfirmations_DepositConfirmedNeverLeaksToAnotherAccount(t *testing.T) {
	f := newDepositEventsFixture(t)
	otherAccount := mocks.InsertAccount(t, "other tenant")
	otherWallet := mocks.InsertWalletWithAccount(t, "eth", &otherAccount.ID)
	events := []string{depositConfirmedEvent}

	owner := mocks.InsertScopedWebhookConfig(t, "https://owner.test/hook", depositHookSecret, events, &f.accountID, nil)
	ownerWallet := mocks.InsertScopedWebhookConfig(t, "https://owner-wallet.test/hook", depositHookSecret, events, nil, &f.wallet.ID)
	legacy := mocks.InsertScopedWebhookConfig(t, "https://legacy.test/hook", depositHookSecret, events, nil, nil)
	otherTenant := mocks.InsertScopedWebhookConfig(t, "https://other-tenant.test/hook", depositHookSecret, events, &otherAccount.ID, nil)
	otherTenantWallet := mocks.InsertScopedWebhookConfig(t, "https://other-wallet.test/hook", depositHookSecret, events, nil, &otherWallet.ID)

	mocks.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", halfEtherBaseUnits, depositBlock)
	if err := f.svc.updateConfirmations(context.Background(), "eth", f.adapter, confirmedAtBlock); err != nil {
		t.Fatalf("updateConfirmations: %v", err)
	}

	delivered := map[uuid.UUID]bool{}
	for _, event := range depositWebhookRows(t) {
		delivered[*event.WebhookConfigID] = true
	}
	for _, cfg := range []models.WebhookConfig{otherTenant, otherTenantWallet} {
		if delivered[cfg.ID] {
			t.Fatalf("deposit.confirmed leaked to %s", cfg.URL)
		}
	}
	for _, cfg := range []models.WebhookConfig{owner, ownerWallet, legacy} {
		if !delivered[cfg.ID] {
			t.Fatalf("deposit.confirmed missing for %s", cfg.URL)
		}
	}
	if len(delivered) != 3 {
		t.Fatalf("delivered to %d configs, want 3", len(delivered))
	}
}

func TestUpdateConfirmations_DepositConfirmedCarriesDecimalAmountAndBaseUnits(t *testing.T) {
	f := newDepositEventsFixture(t)
	mocks.InsertScopedWebhookConfig(t, "https://owner.test/hook", depositHookSecret, []string{depositConfirmedEvent}, &f.accountID, nil)
	tx := mocks.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", halfEtherBaseUnits, depositBlock)

	if err := f.svc.updateConfirmations(context.Background(), "eth", f.adapter, confirmedAtBlock); err != nil {
		t.Fatalf("updateConfirmations: %v", err)
	}

	rows := depositWebhookRows(t)
	if len(rows) != 1 {
		t.Fatalf("got %d deposit.confirmed rows, want 1", len(rows))
	}
	var envelope struct {
		Type string                `json:"type"`
		Data depositevents.Payload `json:"data"`
	}
	if err := json.Unmarshal([]byte(rows[0].Payload), &envelope); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	data := envelope.Data
	if envelope.Type != depositConfirmedEvent || data.TransactionID != tx.ID.String() {
		t.Fatalf("unexpected envelope type=%q transaction=%q", envelope.Type, data.TransactionID)
	}
	if data.Amount != "0.5" || data.AmountFormat != depositevents.AmountFormat {
		t.Fatalf("amount = %q (%q), want 0.5 decimal", data.Amount, data.AmountFormat)
	}
	if data.AmountBaseUnits != halfEtherBaseUnits || data.Decimals != etherDecimals {
		t.Fatalf("base units = %q decimals = %d", data.AmountBaseUnits, data.Decimals)
	}
	if data.Status != "confirmed" || data.Confirmations != int(confirmedAtBlock-depositBlock+1) {
		t.Fatalf("status = %q confirmations = %d", data.Status, data.Confirmations)
	}
}

func TestDepositConfirmed_RedeliveryIsDeduplicatedPerConfig(t *testing.T) {
	f := newDepositEventsFixture(t)
	mocks.InsertScopedWebhookConfig(t, "https://owner.test/hook", depositHookSecret, []string{depositConfirmedEvent}, &f.accountID, nil)
	mocks.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", halfEtherBaseUnits, depositBlock)

	if err := f.svc.updateConfirmations(context.Background(), "eth", f.adapter, confirmedAtBlock); err != nil {
		t.Fatalf("updateConfirmations: %v", err)
	}
	var confirmed models.Transaction
	if err := facades.Orm().Query().Where("wallet_id = ?", f.wallet.ID).First(&confirmed); err != nil {
		t.Fatalf("load deposit: %v", err)
	}
	if err := f.publisher.Publish(context.Background(), types.EventDepositConfirmed, confirmed); err != nil {
		t.Fatalf("republish: %v", err)
	}

	if rows := depositWebhookRows(t); len(rows) != 1 {
		t.Fatalf("got %d deposit.confirmed rows after redelivery, want 1", len(rows))
	}
}
