// Package ramp holds the provider-agnostic services that sit above the ramp
// adapters: webhook ingestion and the reconciliation sweep.
//
// Nothing here knows a provider's wire format. Everything arrives already
// normalized through the adapter contract, which is what lets one processor
// serve every provider instead of one per integration.
package ramp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	pushsvc "github.com/macrowallets/waas/packages/rampengine/app/services/push"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// ErrEventNotFound means the queued event id no longer resolves to a row.
var ErrEventNotFound = errors.New("ramp webhook: event not found")

// WebhookProcessor applies a stored, already-verified provider event.
//
// Verification happened at ingest, where the signature headers existed. By the
// time a row reaches here it is in our own database, so the processor decodes
// rather than re-verifies — see ramp.RampAdapter.DecodeWebhook.
type WebhookProcessor struct {
	factory    *ramp.Factory
	rampConfig *ramp.Config
	events     repositories.WebhookEventRepository
	integRepo  repositories.CustomerProviderIntegrationRepository
	accounts   repositories.CustomerVirtualAccountRepository
	// audience tells every user who acts for a customer what an event changed.
	audience  broadcast.Audience
	notifier  pushsvc.Notifier
	features  contracts.Feature
	deliverer OnrampDeliverer
	settler   OfframpSettler
	bindings  BindingSource
	attention AttentionMarker
	addresses repositories.UserLiquidationAddressRepository
	// kycRefresher re-syncs a customer's KYC row when its bank account changes
	// at the provider; nil leaves those events recorded only. See
	// applyExternalAccount.
	kycRefresher KycRefresher
}

// AttentionMarker raises the attention mark of a transaction. The transaction
// repository satisfies it.
type AttentionMarker interface {
	MarkAttention(txID uint, code string, now time.Time) (bool, error)
}

// WebhookProcessorDeps is what a WebhookProcessor is built from.
type WebhookProcessorDeps struct {
	Factory *ramp.Factory
	// Config supplies the on-ramp delivery attempt ceiling.
	Config       *ramp.Config
	Events       repositories.WebhookEventRepository
	Integrations repositories.CustomerProviderIntegrationRepository
	Accounts     repositories.CustomerVirtualAccountRepository
	// Members is who acts for each customer, who hears of what its events
	// change.
	Members     repositories.CustomerMemberRepository
	Broadcaster broadcast.Broadcaster
	Notifier    pushsvc.Notifier
	Features    contracts.Feature
	Deliverer   OnrampDeliverer
	// Settler may be nil: managed-wallet deposits are then not applied.
	Settler OfframpSettler
	// Bindings is the stored binding of a bank payout. A payout event is
	// applied only against it; without one no bank payout event is applied.
	Bindings BindingSource
	// Attention marks a payout whose event does not match its binding. Without
	// one the mismatch is only logged.
	Attention AttentionMarker
	// Addresses resolves a drain to the liquidation address it paid through.
	Addresses repositories.UserLiquidationAddressRepository
}

// NewWebhookProcessor wires the processor.
func NewWebhookProcessor(d WebhookProcessorDeps) *WebhookProcessor {
	return &WebhookProcessor{
		factory: d.Factory, rampConfig: d.Config, events: d.Events, integRepo: d.Integrations, accounts: d.Accounts,
		audience: broadcast.NewAudience(d.Broadcaster, d.Members), notifier: d.Notifier, features: d.Features,
		deliverer: d.Deliverer, settler: d.Settler, bindings: d.Bindings, attention: d.Attention, addresses: d.Addresses,
	}
}

// announce tells every user who acts for the order's customer it changed: the
// live stream always, and their devices when the change ended it.
func (p *WebhookProcessor) announce(tx *models.Transaction) {
	p.audience.Transaction(tx)
	p.notifier.TransactionUpdated(tx)
}

func (p *WebhookProcessor) ozavDeliveryMaxAttempts() int {
	if p.rampConfig == nil {
		return ramp.DefaultOzavDeliveryMaxAttempts
	}
	return p.rampConfig.OzavDeliveryMaxAttempts()
}

// Process applies one stored event and stamps the outcome on its row. An event
// already carrying a clean stamp is a no-op: the sweep, the queue's own retry
// and an operator's Reprocess all dispatch by event id, and applying one twice
// moves the transaction twice and tells the user twice.
//
// The entity write and the stamp commit together. Apart, a failure between them
// leaves the change already applied and the event still unprocessed, so
// `webhooks:reprocess` re-dispatches it and the user is told twice.
//
// A failed apply is stamped too, on its own: its writes are rolled back, and a
// processed event carrying an error is what makes the failure visible to the
// operator instead of it disappearing into a queue log.
//
// The broadcast fires only after the commit. Publishing from inside the
// transaction would announce a state the rollback then erases.
//
// Account events re-read the provider before the transaction opens — the same
// two-phase shape Provision uses. Holding Postgres open across the Bridge
// client's 30s timeout is the pattern #15 called a blocker.
func (p *WebhookProcessor) Process(ctx context.Context, eventID uint) error {
	event, err := p.events.FindByID(eventID)
	if err != nil {
		return fmt.Errorf("ramp webhook: load event: %w", err)
	}
	if event == nil {
		return ErrEventNotFound
	}

	result, err := p.decodeEvent(ctx, event)
	if err != nil {
		return p.fail(event, err)
	}

	// Account events need the provider's current view. Read it before opening
	// the transaction — the same two-phase pattern Provision uses.
	prefetched, err := p.prefetchAccountView(ctx, event, result)
	if err != nil {
		return p.fail(event, err)
	}
	if err := p.prefetchDepositOwner(ctx, event, result); err != nil {
		return p.fail(event, err)
	}

	var publish func()
	// The stamp is re-read under the same SELECT … FOR UPDATE the admin path
	// takes, and inside the transaction, so a run that starts while this one is
	// still applying waits for the commit instead of reading the row as
	// unprocessed and passing the same guard.
	processErr := p.events.WithLocked(event.ID, func(locked *models.WebhookEvent, db repositories.WebhookTx) error {
		if locked == nil {
			return ErrEventNotFound
		}
		if locked.Applied() {
			return nil
		}

		announce, err := p.apply(ctx, db, event, result, prefetched)
		if err != nil {
			return err
		}
		stamp(event, nil)
		if err := db.UpdateEvent(event); err != nil {
			return fmt.Errorf("ramp webhook: stamp event: %w", err)
		}
		publish = announce
		return nil
	})
	var lockErr *repositories.LockError
	if errors.As(processErr, &lockErr) {
		processErr = fmt.Errorf("ramp webhook: lock event: %w", processErr)
	}
	if processErr != nil {
		return p.fail(event, processErr)
	}
	if publish != nil {
		publish()
	}
	return nil
}

// fail stamps a processing error on the event once the apply has rolled back,
// so the failure is visible to operators instead of vanishing into a queue
// log. It stamps the row as it stands under the event lock, and only while it
// is still unapplied: a duplicate run may have applied the event since, and
// its stamp and link are the truth. The in-memory event cannot be written
// instead — apply() may have linked it to a row the rollback took back.
func (p *WebhookProcessor) fail(event *models.WebhookEvent, cause error) error {
	err := p.events.WithLocked(event.ID, func(locked *models.WebhookEvent, db repositories.WebhookTx) error {
		if locked == nil || locked.Applied() {
			return nil
		}
		stamp(locked, cause)
		return db.UpdateEvent(locked)
	})
	if err != nil {
		return fmt.Errorf("ramp webhook: stamp event: %w", err)
	}
	return cause
}

// stamp records the outcome on the event row.
func stamp(event *models.WebhookEvent, cause error) {
	now := time.Now()
	event.ProcessedAt = &now
	if cause == nil {
		event.ProcessingError = nil
		return
	}
	message := cause.Error()
	event.ProcessingError = &message
}

// decodeEvent turns the stored payload into a typed result. Verification already
// happened at ingest; this is parse-only and stays off any open transaction.
func (p *WebhookProcessor) decodeEvent(ctx context.Context, event *models.WebhookEvent) (*ramp.WebhookResult, error) {
	adapter, err := p.factory.Get(event.Provider)
	if err != nil {
		return nil, err
	}
	result, err := adapter.DecodeWebhook(ctx, []byte(event.RawPayload))
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: decode: %w", err)
	}
	return result, nil
}

// apply routes the decoded event to the store it updates, returning the
// broadcast the caller runs once the transaction has committed.
func (p *WebhookProcessor) apply(
	ctx context.Context,
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
	view *accountView,
) (func(), error) {
	ramp.LogWebhookLag(event.Provider, result.EventType, result.OccurredAt)

	switch result.Kind {
	case ramp.WebhookKindRamp:
		return p.applyRamp(ctx, db, event, result)
	case ramp.WebhookKindKyc:
		return p.applyKyc(db, event, result)
	case ramp.WebhookKindVirtualAccountDeposit:
		return p.applyVirtualAccountDeposit(ctx, db, event, result)
	case ramp.WebhookKindVirtualAccount:
		return p.applyVirtualAccount(db, event, view)
	case ramp.WebhookKindLiquidationDrain:
		return p.applyLiquidationDrain(ctx, db, event, result)
	case ramp.WebhookKindExternalAccount:
		return p.applyExternalAccount(ctx, event, result)
	case ramp.WebhookKindManagedWalletDeposit:
		return p.applyManagedWalletDeposit(ctx, event, result)
	default:
		// Authentic but unmodelled. Recording it and moving on beats making the
		// provider retry an event we have no handler for.
		return nil, nil
	}
}

// findUnplacedOrder resolves an order by the quote its transfer was made from.
// The provider can make the transfer and still refuse the create (Ozav's
// AMOUNT_BELOW_MINIMUM on POST /transfers), and then gateway_order_id was never
// written; the event's own transfer id is linked to the order when it applies.
func (p *WebhookProcessor) findUnplacedOrder(db repositories.WebhookTx, provider, quoteID string) (*models.Transaction, error) {
	if quoteID == "" {
		return nil, nil
	}
	tx, err := db.FindUnplacedOrderByQuoteForUpdate(provider, quoteID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load unplaced transaction: %w", err)
	}
	return tx, nil
}

// applyRamp moves a transaction's status.
func (p *WebhookProcessor) applyRamp(ctx context.Context, db repositories.WebhookTx, event *models.WebhookEvent, result *ramp.WebhookResult) (func(), error) {
	if result.ProviderOrderID == "" {
		return nil, nil
	}
	tx, err := db.FindOrderForUpdate(event.Provider, result.ProviderOrderID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load transaction: %w", err)
	}
	if tx == nil {
		// It may name the SECOND movement instead. An order-first on-ramp
		// finishes with its own transfer, whose id lives in delivery_order_id;
		// its lifecycle events would otherwise fail here forever, one
		// unprocessable event each, which is the only signal that a user's
		// crypto never left the provider's custody.
		delivered, err := db.FindDeliveryForUpdate(event.Provider, result.ProviderOrderID)
		if err != nil {
			return nil, fmt.Errorf("ramp webhook: load delivery transaction: %w", err)
		}
		if delivered != nil {
			return p.applyDelivery(db, event, result, delivered)
		}
		tx, err = p.findUnplacedOrder(db, event.Provider, result.ProviderQuoteID)
		if err != nil {
			return nil, err
		}
	}
	if tx == nil {
		// The create may still be in flight: the row is written before the
		// upstream call, but a provider can deliver the first event before our
		// own response lands. Reconciliation catches these.
		return nil, fmt.Errorf("ramp webhook: no transaction for %s order %s", event.Provider, result.ProviderOrderID)
	}

	// A bank payout's event is applied only for the identity the stored binding
	// names. The payload has no quote, network, token or amount to check, so
	// customer, bank account and wallet are what it can be held to.
	if tx.FundedByAuthorization() {
		ok, mismatch, err := p.payoutEventMatches(tx, result)
		if err != nil {
			return nil, err
		}
		if !ok {
			if mismatch {
				return p.markBindingMismatch(tx), nil
			}
			return nil, nil
		}
	}

	// A terminal order does not walk backwards. The only permitted exit is the
	// refund path, which is a real transition and not a late duplicate.
	//
	// Nothing about the row moves, but the event may still be what finishes it:
	// a read that re-checked the provider can complete an on-ramp before its
	// event lands, and that read's delivery can fail. The pay-in is settled
	// either way, so the second hop is asked for again — a no-op for anything
	// that does not await one.
	if models.IsTerminalStatus(tx.Status) && !models.CanLeaveTerminal(tx.Status, result.Status) {
		return func() { DeliverSettledOnramp(ctx, p.deliverer, tx) }, nil
	}

	order := &ramp.RampOrder{
		Status:          result.Status,
		ProviderOrderID: result.ProviderOrderID,
		TxHash:          result.TxHash,
		Amounts:         map[string]string{},
	}
	if result.ExchangeRate != "" {
		order.Amounts["exchange_rate"] = result.ExchangeRate
	}
	if result.SourceAmount != "" {
		order.Amounts["source_amount"] = result.SourceAmount
	}
	if result.DestinationAmount != "" {
		order.Amounts["destination_amount"] = result.DestinationAmount
	}
	if result.ExchangeFee != "" {
		order.Amounts["exchange_fee"] = result.ExchangeFee
	}
	// A payout under refund review never goes back to processing: the status
	// stays, whatever else this read says.
	if models.RegressesStatus(tx.Status, order.Status) {
		order.Status = ""
	}
	ApplyOrder(tx, order)
	if result.FailureReason != "" {
		tx.FailureReason = &result.FailureReason
	}
	if ShouldSetCompletedAt(tx, tx.Status) && tx.CompletedAt == nil {
		completed := time.Now()
		if result.OccurredAt != nil {
			completed = *result.OccurredAt
		}
		tx.CompletedAt = &completed
	}
	if err := db.UpdateTransaction(tx); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist transaction: %w", err)
	}

	event.TransactionID = &tx.ID
	return func() {
		p.announce(tx)
		DeliverSettledOnramp(ctx, p.deliverer, tx)
	}, nil
}

// applyDelivery records the outcome of an on-ramp's second movement. What the
// outcome means is ApplyDeliveryOutcome's to decide, shared with the read path
// and the reconciler so a lost event and a delivered one end the same way.
func (p *WebhookProcessor) applyDelivery(
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
	tx *models.Transaction,
) (func(), error) {
	event.TransactionID = &tx.ID
	out := DeliveryOutcome{
		ProviderOrderID: result.ProviderOrderID,
		Status:          result.Status,
		TxHash:          result.TxHash,
		Failed:          result.DeliveryFailed,
		FailureReason:   result.FailureReason,
		OccurredAt:      result.OccurredAt,
	}
	maxAttempts := p.ozavDeliveryMaxAttempts()
	if !ApplyDeliveryOutcome(tx, out, maxAttempts) {
		return nil, nil
	}
	if err := db.UpdateTransaction(tx); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist delivery outcome: %w", err)
	}
	// The order's status stands, so this is news for the live screen only: the
	// devices already heard how the order ended. The failure is logged here,
	// after the commit, so a rolled-back event announces no attempt.
	return func() {
		LogDeliveryFailure(context.Background(), tx, out, maxAttempts)
		p.audience.Transaction(tx)
	}, nil
}

// payoutEventMatches holds a payout event's identity against the stored
// binding. A missing binding or any mismatch applies nothing and is flagged
// for an operator, with ids only.
//
// mismatch is true when a stored binding disagrees with the event (the case the
// attention mark is raised for); a missing binding is only logged.
func (p *WebhookProcessor) payoutEventMatches(tx *models.Transaction, result *ramp.WebhookResult) (ok, mismatch bool, err error) {
	var binding *ramp.OrderBinding
	if p.bindings != nil {
		if binding, err = p.bindings.PayoutBinding(tx.ID); err != nil {
			return false, false, fmt.Errorf("ramp webhook: read payout binding: %w", err)
		}
	}
	if binding == nil {
		facades.Log().Errorf("ramp.attention: payout %s has no stored binding, event not applied", result.ProviderOrderID)
		return false, false, nil
	}
	// The binding is never empty, and the customer and the wallet must be named
	// by the event and match: an empty field on either side never matches, or
	// it would equal a payload that omits it. The bank account is held only
	// where the event names it. BlindPay's payout events do not carry it (S10,
	// every real delivery omitted it), so requiring it would leave no payout
	// event applicable; the order the event belongs to is already fixed by the
	// signed delivery and the payout id, and the reconciler's bound read holds
	// the bank account.
	pairs := [][2]string{
		{result.ProviderCustomerID, binding.ProviderCustomerID},
		{result.SenderWallet, binding.SenderWallet},
	}
	if result.BankAccountID != "" {
		pairs = append(pairs, [2]string{result.BankAccountID, binding.BankAccountID})
	}
	for _, pair := range pairs {
		if pair[1] == "" || pair[0] != pair[1] {
			facades.Log().Errorf("ramp.attention: payout %s event does not match the stored binding, not applied", result.ProviderOrderID)
			return false, true, nil
		}
	}
	return true, false, nil
}

// markBindingMismatch answers the post-commit step that raises the attention
// mark. It runs after the commit because the order row is locked by the
// event's transaction, and the repository writes on its own connection. A
// failure is logged and never fails the event: the outcome is already "nothing
// applied", and a retry would only read the same mismatch again.
func (p *WebhookProcessor) markBindingMismatch(tx *models.Transaction) func() {
	if p.attention == nil {
		return nil
	}
	txID, orderID := tx.ID, tx.GatewayOrderID
	return func() {
		if _, err := p.attention.MarkAttention(txID, models.AttentionBindingMismatch, time.Now()); err != nil {
			facades.Log().Errorf("ramp.attention: mark payout %s as not matching its binding: %v", orderID, err)
		}
	}
}
