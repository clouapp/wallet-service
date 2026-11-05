package repositories

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// WebhookTx is the transaction a webhook event is applied in: every write an
// event can cause, and the save of the event itself, commit or roll back
// together. Apart, a failure between an entity write and the stamp leaves the
// change applied and the event unprocessed, and `webhooks:reprocess` applies it
// again. WebhookEventRepository.WithLocked opens it, so the processor decides
// what to write without ever holding the query it writes through.
type WebhookTx interface {
	CreateTransaction(tx *models.Transaction) error
	UpdateTransaction(tx *models.Transaction) error
	// The Find*ForUpdate lookups load the row an event changes under SELECT …
	// FOR UPDATE. The event lock serializes runs of one event, not two events
	// about one row: without the row lock, two applies in READ COMMITTED read
	// the same snapshot, and the later write silently discards the other — a
	// completed order walked back to processing that way. Locks are always
	// taken event first, then the one row the event changes.
	FindDepositForUpdate(provider, depositID string) (*models.Transaction, error)
	FindOrderForUpdate(provider, gatewayOrderID string) (*models.Transaction, error)
	// FindDeliveryForUpdate resolves the order whose SECOND movement carries
	// this provider id. A delivery is its own transfer upstream, so its
	// lifecycle events name an id that gateway_order_id — the pay-in's —
	// never matches.
	FindDeliveryForUpdate(provider, deliveryOrderID string) (*models.Transaction, error)
	// FindUnplacedOrderByQuoteForUpdate resolves an order whose transfer the
	// provider made while refusing the create, so gateway_order_id was never
	// written: the order still without one whose estimate holds this quote.
	FindUnplacedOrderByQuoteForUpdate(provider, providerQuoteID string) (*models.Transaction, error)
	FindKycForUpdate(customerID uint, provider string) (*models.CustomerProviderKyc, error)
	FindVirtualAccountForUpdate(id uint) (*models.CustomerVirtualAccount, error)
	UpdateKyc(row *models.CustomerProviderKyc) error
	// UpdateIntegration writes a link while it is live (a provider-side
	// deletion switches it off).
	UpdateIntegration(link *models.CustomerProviderIntegration) error
	UpdateVirtualAccount(account *models.CustomerVirtualAccount) error
	UpdateEvent(event *models.WebhookEvent) error
}

type webhookTx struct{ q orm.Query }

func (t webhookTx) CreateTransaction(tx *models.Transaction) error {
	return (&transactionRepository{}).CreateTx(t.q, tx)
}

func (t webhookTx) UpdateTransaction(tx *models.Transaction) error {
	return (&transactionRepository{}).UpdateTx(t.q, tx)
}

func (t webhookTx) FindDepositForUpdate(provider, depositID string) (*models.Transaction, error) {
	return (&transactionRepository{}).FindByProviderAndDepositIDForUpdate(t.q, provider, depositID)
}

func (t webhookTx) FindOrderForUpdate(provider, gatewayOrderID string) (*models.Transaction, error) {
	return first[models.Transaction](t.q.LockForUpdate().
		Where("provider", provider).
		Where("gateway_order_id", gatewayOrderID), "webhook tx: find order for update")
}

func (t webhookTx) FindDeliveryForUpdate(provider, deliveryOrderID string) (*models.Transaction, error) {
	return first[models.Transaction](t.q.LockForUpdate().
		Where("provider", provider).
		Where("delivery_order_id", deliveryOrderID), "webhook tx: find delivery for update")
}

func (t webhookTx) FindUnplacedOrderByQuoteForUpdate(provider, providerQuoteID string) (*models.Transaction, error) {
	estimate, err := first[models.RampEstimate](t.q.
		Where("provider", provider).
		Where("provider_quote_id", providerQuoteID), "webhook tx: find estimate by quote")
	if err != nil || estimate == nil {
		return nil, err
	}
	return first[models.Transaction](t.q.LockForUpdate().
		Where("provider", provider).
		Where("estimate_id", estimate.ID).
		Where("gateway_order_id", ""), "webhook tx: find unplaced order by quote for update")
}

func (t webhookTx) FindKycForUpdate(customerID uint, provider string) (*models.CustomerProviderKyc, error) {
	row, err := first[models.CustomerProviderKyc](t.q.LockForUpdate().
		Where("customer_id", customerID).
		Where("provider", provider), "webhook tx: find kyc for update")
	if err != nil || row == nil {
		return row, err
	}
	// Marked as the finders mark a row, in the webhook's transaction: a
	// business's verdict arriving by webhook is the company's.
	if err := markBusiness(t.q, customerID, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (t webhookTx) FindVirtualAccountForUpdate(id uint) (*models.CustomerVirtualAccount, error) {
	return first[models.CustomerVirtualAccount](t.q.LockForUpdate().Where("id", id), "webhook tx: find virtual account for update")
}

func (t webhookTx) UpdateKyc(row *models.CustomerProviderKyc) error {
	return (&customerProviderKycRepository{}).UpdateTx(t.q, row)
}

func (t webhookTx) UpdateIntegration(link *models.CustomerProviderIntegration) error {
	written, err := updateLiveLink(t.q, link)
	if err != nil {
		return fmt.Errorf("webhook tx: update integration: %w", err)
	}
	if !written {
		return fmt.Errorf("webhook tx: update integration: %w", ErrIntegrationGone)
	}
	return nil
}

func (t webhookTx) UpdateVirtualAccount(account *models.CustomerVirtualAccount) error {
	return (&customerVirtualAccountRepository{}).UpdateTx(t.q, account)
}

func (t webhookTx) UpdateEvent(event *models.WebhookEvent) error {
	return (&webhookEventRepository{}).UpdateTx(t.q, event)
}
