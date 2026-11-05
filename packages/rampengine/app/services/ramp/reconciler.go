package ramp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	pushsvc "github.com/macrowallets/waas/packages/rampengine/app/services/push"
)

// errPayoutAttention marks a bank payout that was not read or not applied for
// want of a matching stored binding. It is not a failure of the provider: the
// row waits for an operator, and the sweep must not keep it at the head.
var errPayoutAttention = errors.New("ramp reconcile: payout needs attention")

// Reconciler is the safety net behind webhook delivery.
//
// Webhooks alone are not a status machine. A delivery can be dropped, delayed
// past a retry budget, or accepted while the queue is down — and an order left
// in `awaiting_funds` forever is money the user believes is in flight. This
// sweeps what the event stream missed.
type Reconciler struct {
	router *ramp.Router
	// factory and integRepo serve the deposit branch, which deliberately does
	// not route. See depositAdapter.
	factory   *ramp.Factory
	integRepo repositories.CustomerProviderIntegrationRepository
	txRepo    repositories.TransactionRepository
	accounts  repositories.CustomerVirtualAccountRepository
	// audience tells every user who acts for an order's customer it moved.
	audience broadcast.Audience
	notifier pushsvc.Notifier
	// config supplies the on-ramp delivery attempt ceiling.
	config *ramp.Config
	// deliverer finishes an on-ramp whose crypto is still in the provider's
	// custody. It is the same port the webhook processor holds: the webhook is
	// the fast path and gets one attempt, and this sweep is what retries it.
	deliverer OnrampDeliverer
	settler   OfframpSettler
	// bindings is the stored binding a bank payout is read against.
	bindings BindingSource
	// addresses resolves transactions.liquidation_address_id for the drain
	// branch, which does not route either. See ResolveDrainAdapter.
	addresses repositories.UserLiquidationAddressRepository
}

// OfframpSettler enqueues payout for funded Ozav off-ramps still awaiting settlement.
type OfframpSettler interface {
	// EnqueueSettleIfFunded queues the payout when the managed wallet covers
	// the order and reports whether it did.
	EnqueueSettleIfFunded(ctx context.Context, tx *models.Transaction) (bool, error)
	// ApplyManagedWalletDeposit claims a deposit for the order it funds and
	// returns the enqueue to run once the caller's transaction commits.
	ApplyManagedWalletDeposit(ctx context.Context, customerID uint, dep ramp.ManagedWalletDeposit) (func(), error)
}

// ReconcilerDeps are the Reconciler's collaborators, filled by field name.
// Deliverer and Settler may be nil: nothing is delivered or settled then.
type ReconcilerDeps struct {
	Router       *ramp.Router
	Factory      *ramp.Factory
	Integrations repositories.CustomerProviderIntegrationRepository
	Transactions repositories.TransactionRepository
	Accounts     repositories.CustomerVirtualAccountRepository
	// Audience tells every user who acts for an order's customer it moved.
	Audience  broadcast.Audience
	Notifier  pushsvc.Notifier
	Config    *ramp.Config
	Deliverer OnrampDeliverer
	Settler   OfframpSettler
	// Bindings is the stored binding of a bank payout; without it a bank
	// payout is never read.
	Bindings BindingSource
	// Addresses resolves a USD payout's liquidation address for the drain
	// branch.
	Addresses repositories.UserLiquidationAddressRepository
}

// NewReconciler builds the sweep from its collaborators; see ReconcilerDeps for
// which may be nil.
func NewReconciler(d ReconcilerDeps) *Reconciler {
	return &Reconciler{
		router: d.Router, factory: d.Factory, integRepo: d.Integrations,
		txRepo: d.Transactions, accounts: d.Accounts, audience: d.Audience,
		notifier: d.Notifier,
		config:   d.Config, deliverer: d.Deliverer, settler: d.Settler,
		bindings:  d.Bindings,
		addresses: d.Addresses,
	}
}

// announce tells every user who acts for the order's customer it changed: the
// live stream always, and their devices when the change ended it.
func (r *Reconciler) announce(tx *models.Transaction) {
	r.audience.Transaction(tx)
	r.notifier.TransactionUpdated(tx)
}

// ReconcileReport is what one sweep did.
type ReconcileReport struct {
	Scanned int
	Updated int
	Expired int
	// Delivered counts second hops this sweep completed — settled on-ramps
	// whose crypto was still in the provider's custody.
	Delivered int
	Failed    int
	// Attention counts bank payouts the sweep would not apply: no stored
	// binding, or one the provider's payout does not match. Each is logged
	// with ramp.attention and touched so it leaves the head of the queue.
	Attention int
}

// Sweep reconciles stale non-terminal orders.
//
// `staleAfter` keeps the sweep off orders the event stream is still likely to
// resolve on its own: re-reading an order that was created ninety seconds ago
// costs a provider call to learn what a webhook is about to tell us anyway.
func (r *Reconciler) Sweep(ctx context.Context, staleAfter time.Duration, limit int) (*ReconcileReport, error) {
	orders, err := r.txRepo.FindStaleRampOrders(time.Now().Add(-staleAfter), limit)
	if err != nil {
		return nil, fmt.Errorf("ramp reconcile: list stale orders: %w", err)
	}

	report := &ReconcileReport{Scanned: len(orders)}
	// One sweep sees several orders from the same user on the same rail, and
	// each resolve costs three queries (config, integration, KYC). Memoizing
	// for the duration of the sweep makes that 3 instead of 3N. The window is
	// deliberately one sweep: eligibility is admin- and compliance-state that
	// can change, and a cache outliving the batch would route on a stale answer.
	routes := newRouteCache(r)
	for i := range orders {
		tx := &orders[i]
		// A funded order is settled, never expired: its deposit may have been
		// seen only through the balance, with no webhook to record it.
		settled, err := r.settleIfFunded(ctx, tx)
		if err != nil {
			report.Failed++
			facades.Log().Warningf("ramp reconcile: settle check for order %s: %v", tx.ExternalID, err)
			continue
		}
		if settled {
			continue
		}
		// An order the provider issued is asked about once before its window
		// is closed: a payment that landed in the last minutes, whose event is
		// late or lost, would otherwise be expired over funds the provider
		// holds, and an expired row is never swept again. Funds on the read are
		// applied and the order does not expire; an unfunded read, or one that
		// fails, leaves the local decision as it was.
		if dueForExpiry(tx) && tx.GatewayOrderID != "" {
			r.refreshThenExpire(ctx, routes, tx, report)
			continue
		}
		// Expiry is decided locally. The deposit window belongs to the
		// instructions we handed the user, and an order nobody funded before it
		// closed is expired whether or not the provider says so yet.
		expired, err := r.expire(tx)
		if err != nil {
			report.Failed++
			facades.Log().Errorf("ramp reconcile: expire order %s: %v", tx.ExternalID, err)
			continue
		}
		if expired {
			report.Expired++
			continue
		}

		changed, err := r.refresh(ctx, routes, tx)
		if errors.Is(err, errPayoutAttention) {
			r.parkForAttention(tx, report)
			continue
		}
		if err != nil {
			report.Failed++
			// The order is unchanged and will be picked up by the next sweep.
			// One provider being down must not abort the rest of the batch.
			facades.Log().Warningf("ramp reconcile: refresh order %s: %v", tx.ExternalID, err)
			continue
		}
		if changed {
			report.Updated++
			r.announce(tx)
		}
	}
	// Deliveries first: one whose failure only this sweep sees is due again
	// right away, in sweepUndelivered.
	r.sweepDeliveries(ctx, staleAfter, limit, report)
	r.sweepUndelivered(ctx, limit, report)
	ramp.LogReconcile(report.Scanned, report.Updated, report.Expired, report.Failed+report.Attention)
	return report, nil
}

// RefreshOne re-reads a single order at the provider. It applies the same
// provider status transition the sweep uses but does not run local expiry;
// use the sweep for deposit-window expiry.
//
// A refresh that completes an on-ramp asks for its delivery here, because no
// sweepUndelivered follows it the way one follows Sweep's refreshes.
func (r *Reconciler) RefreshOne(ctx context.Context, tx *models.Transaction) (bool, error) {
	if settled, err := r.settleIfFunded(ctx, tx); err != nil || settled {
		return false, err
	}
	changed, err := r.refresh(ctx, newRouteCache(r), tx)
	if errors.Is(err, errPayoutAttention) {
		// Logged; nothing is written for a payout that is not the bound one.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if changed {
		r.announce(tx)
		DeliverSettledOnramp(ctx, r.deliverer, tx)
	}
	return changed, nil
}

// deliveringProviders lists the provider codes whose adapter implements the
// second hop.
//
// The query is scoped to them because most providers have nothing to deliver,
// and an unscoped one would rescan every on-ramp they ever completed to do
// nothing with it. Reading the capability off the factory means the list
// follows the adapters rather than a constant somebody has to remember.
func (r *Reconciler) deliveringProviders() []string {
	codes := make([]string, 0, 2)
	for _, code := range r.factory.Codes() {
		adapter, err := r.factory.Get(code)
		if err != nil {
			continue
		}
		if _, ok := adapter.(ramp.OnrampDeliveryAdapter); ok {
			codes = append(codes, code)
		}
	}
	return codes
}

// sweepUndelivered finishes settled on-ramps the webhook did not.
//
// The webhook is the fast path and gets exactly one attempt; an order that is
// already terminal receives no further events, so a send that failed there
// would sit forever with the user's money in the provider's custody. This is
// the retry.
//
// A failure is counted and logged, never returned: one provider refusing must
// not abort the batch, and the sweep's caller is a scheduled command whose job
// is to keep going.
func (r *Reconciler) sweepUndelivered(ctx context.Context, limit int, report *ReconcileReport) {
	if r.deliverer == nil {
		return
	}
	providers := r.deliveringProviders()
	if len(providers) == 0 {
		return
	}
	orders, err := r.txRepo.FindUndeliveredOnramps(providers, r.config.OzavDeliveryMaxAttempts(), limit)
	if err != nil {
		facades.Log().Errorf("ramp reconcile: list undelivered onramps: %v", err)
		return
	}
	for i := range orders {
		tx := &orders[i]
		if err := r.deliverer.Deliver(ctx, tx); err != nil {
			report.Failed++
			facades.Log().Warningf("ramp reconcile: deliver onramp %s: %v", tx.ExternalID, err)
			// Touch the row so it goes to the back of the queue. The query is
			// ordered oldest-first on updated_at and a failure writes nothing
			// else, so without this a row that fails deterministically — an
			// insufficient balance, a customer whose custody model is wrong —
			// would hold the head of every future sweep. After `limit` such
			// rows the safety net stops running for everyone, silently.
			if err := r.txRepo.Update(tx); err != nil {
				facades.Log().Errorf("ramp reconcile: touch undelivered onramp %s: %v", tx.ExternalID, err)
			}
			continue
		}
		report.Delivered++
	}
}

// parkForAttention counts a payout the sweep would not apply and moves it to the
// back of the oldest-first queue by touching updated_at only, so a row that
// stays wrong cannot hold the head and starve the limit.
func (r *Reconciler) parkForAttention(tx *models.Transaction, report *ReconcileReport) {
	report.Attention++
	if err := r.txRepo.TouchUpdatedAt(tx.ID); err != nil {
		facades.Log().Errorf("ramp reconcile: touch payout %s needing attention: %v", tx.ExternalID, err)
	}
}

// refreshThenExpire is Sweep's step for an order due to expire that the
// provider issued: the provider's read first, then the local expiry, which
// passes over the order if the read moved it off an unfunded status.
func (r *Reconciler) refreshThenExpire(ctx context.Context, routes *routeCache, tx *models.Transaction, report *ReconcileReport) {
	changed, err := r.refresh(ctx, routes, tx)
	if errors.Is(err, errPayoutAttention) {
		r.parkForAttention(tx, report)
		err = nil
	}
	if err != nil {
		// Counted, but the order may still expire below: a provider that cannot
		// be read leaves the local decision standing.
		report.Failed++
		facades.Log().Warningf("ramp reconcile: refresh order %s before expiry: %v", tx.ExternalID, err)
	}
	expired, err := r.expire(tx)
	if err != nil {
		report.Failed++
		facades.Log().Errorf("ramp reconcile: expire order %s: %v", tx.ExternalID, err)
		return
	}
	if expired {
		report.Expired++
		return
	}
	if changed {
		report.Updated++
		r.announce(tx)
	}
}

// dueForExpiry reports whether an order's deposit window passed while it was
// still unfunded.
func dueForExpiry(tx *models.Transaction) bool {
	if tx.ExpiresAt == nil || tx.ExpiresAt.After(time.Now()) {
		return false
	}
	return tx.Status == models.TxStatusPending || tx.Status == models.TxStatusAwaitingFunds
}

// sweepDeliveries re-reads deliveries that were placed and never reported
// back. The pay-in row is already completed, so FindStaleRampOrders never
// returns it, and without this a lost delivery webhook leaves the order in
// flight until the user happens to open it.
func (r *Reconciler) sweepDeliveries(ctx context.Context, staleAfter time.Duration, limit int, report *ReconcileReport) {
	providers := r.deliveringProviders()
	if len(providers) == 0 {
		return
	}
	orders, err := r.txRepo.FindDeliveriesInFlight(providers, time.Now().Add(-staleAfter), limit)
	if err != nil {
		facades.Log().Errorf("ramp reconcile: list deliveries in flight: %v", err)
		return
	}
	maxAttempts := r.config.OzavDeliveryMaxAttempts()
	for i := range orders {
		tx := &orders[i]
		// Settlement, not eligibility: see Deliver.
		adapter, ref, err := r.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
		if err != nil {
			report.Failed++
			facades.Log().Warningf("ramp reconcile: resolve onramp %s delivery: %v", tx.ExternalID, err)
			continue
		}
		changed, err := RefreshDelivery(ctx, adapter, ref, r.txRepo, tx, maxAttempts)
		if err != nil {
			report.Failed++
			facades.Log().Warningf("ramp reconcile: refresh onramp %s delivery: %v", tx.ExternalID, err)
			continue
		}
		if changed {
			report.Updated++
			// The order's status stands: news for the live screen only.
			r.audience.Transaction(tx)
		}
	}
}

// settleIfFunded queues the payout of an Ozav off-ramp the managed wallet
// already covers, and reports whether it did. An error means funding could not
// be read, and the order is left alone rather than expired on a guess.
func (r *Reconciler) settleIfFunded(ctx context.Context, tx *models.Transaction) (bool, error) {
	if r.settler == nil || tx.Type != models.TransactionTypeOfframp || tx.Provider != models.ProviderOzav ||
		tx.GatewayOrderID != "" {
		return false, nil
	}
	return r.settler.EnqueueSettleIfFunded(ctx, tx)
}

// expire closes an order whose deposit window passed without funding.
//
// Only an unfunded order expires. Once money has arrived the window is no
// longer the deciding fact, and marking it expired would contradict funds the
// provider is already holding.
func (r *Reconciler) expire(tx *models.Transaction) (bool, error) {
	if !dueForExpiry(tx) {
		return false, nil
	}
	if tx.Provider == models.ProviderOzav && tx.Type == models.TransactionTypeOfframp &&
		tx.DepositTxSignature != nil && strings.TrimSpace(*tx.DepositTxSignature) != "" {
		return false, nil
	}

	// tx was read before settleIfFunded's provider call, and a deposit claimed
	// in between must not be overwritten by a full save of the stale row: the
	// write re-checks the row's own status and claim.
	expired, err := r.txRepo.ExpireUnfunded(tx)
	if err != nil {
		return false, fmt.Errorf("ramp reconcile: persist expiry: %w", err)
	}
	if expired {
		r.announce(tx)
	}
	return expired, nil
}

// routeCache memoizes lookups for the length of one sweep. See the note at
// its construction in Sweep for why the window is exactly that.
type routeCache struct {
	router      *ramp.Router
	factory     *ramp.Factory
	integRepo   repositories.CustomerProviderIntegrationRepository
	accounts    repositories.CustomerVirtualAccountRepository
	entries     map[string]routeEntry
	adapters    map[string]depositRoute
	accountByID map[uint]accountEntry
}

type routeEntry struct {
	adapter ramp.ProviderAdapter
	ref     ramp.CustomerRef
	err     error
}

type depositRoute struct {
	standing ramp.VirtualAccountAdapter
	ref      ramp.CustomerRef
	err      error
}

type accountEntry struct {
	account *models.CustomerVirtualAccount
	err     error
}

func newRouteCache(r *Reconciler) *routeCache {
	return &routeCache{
		router:      r.router,
		factory:     r.factory,
		integRepo:   r.integRepo,
		accounts:    r.accounts,
		entries:     map[string]routeEntry{},
		adapters:    map[string]depositRoute{},
		accountByID: map[uint]accountEntry{},
	}
}

// resolve answers from the cache or asks the router once per customer. A
// failure is cached too: a provider the customer is not cleared for will not
// become cleared halfway through a batch, and re-asking would repeat the three
// queries per order.
func (c *routeCache) resolve(
	ctx context.Context,
	customerID uint,
	rail ramp.Rail,
	provider ramp.ProviderCode,
) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
	key := fmt.Sprintf("%d:%s:%s", customerID, rail, provider)
	if entry, ok := c.entries[key]; ok {
		return entry.adapter, entry.ref, entry.err
	}
	adapter, ref, err := c.router.ResolveProvider(ctx, customerID, rail, provider)
	c.entries[key] = routeEntry{adapter: adapter, ref: ref, err: err}
	return adapter, ref, err
}

// resolveSettlement is resolve without the eligibility gate, for following a
// movement already in flight (see Router.ResolveForSettlement). Memoized like
// resolve, under its own key.
func (c *routeCache) resolveSettlement(ctx context.Context, customerID uint, provider ramp.ProviderCode) (ramp.ProviderAdapter, ramp.CustomerRef, error) {
	key := fmt.Sprintf("settle:%d:%s", customerID, provider)
	if entry, ok := c.entries[key]; ok {
		return entry.adapter, entry.ref, entry.err
	}
	adapter, ref, err := c.router.ResolveForSettlement(ctx, customerID, provider)
	c.entries[key] = routeEntry{adapter: adapter, ref: ref, err: err}
	return adapter, ref, err
}

// account answers from the cache or loads the standing-account row once.
// Several deposits into the same account would otherwise re-run FindByID
// per order.
func (c *routeCache) account(id uint) (*models.CustomerVirtualAccount, error) {
	if entry, ok := c.accountByID[id]; ok {
		return entry.account, entry.err
	}
	account, err := c.accounts.FindByID(id)
	if err != nil {
		err = fmt.Errorf("ramp reconcile: load virtual account: %w", err)
	}
	c.accountByID[id] = accountEntry{account: account, err: err}
	return account, err
}

// depositAdapter answers from the cache or resolves the standing-account
// adapter and customer ref once per (customer, provider). It does not route:
// resolveDepositAdapter documents why.
func (c *routeCache) depositAdapter(tx *models.Transaction) (ramp.VirtualAccountAdapter, ramp.CustomerRef, error) {
	key := fmt.Sprintf("%d:%s", tx.CustomerID, tx.Provider)
	if entry, ok := c.adapters[key]; ok {
		return entry.standing, entry.ref, entry.err
	}
	standing, ref, err := ResolveDepositAdapter(c.factory, c.integRepo, tx)
	c.adapters[key] = depositRoute{standing: standing, ref: ref, err: err}
	return standing, ref, err
}

// refresh pulls the provider's view onto the row.
//
// Which question to ask is decided by the row, not by the provider: Bridge
// serves both shapes, and `virtual_account_id` is what says the money arrived in
// a standing account rather than against an order we created.
func (r *Reconciler) refresh(ctx context.Context, routes *routeCache, tx *models.Transaction) (bool, error) {
	if tx.GatewayOrderID == "" {
		// Written before the upstream call and never answered: the create
		// failed after persisting. There is nothing to ask the provider about
		// under an id it never issued, and expire() has already collected it by
		// its reservation deadline.
		return false, nil
	}
	rail := ramp.RailPixPayin
	if tx.Rail != nil {
		rail = ramp.Rail(*tx.Rail)
	}
	// A USD payout is a drain of a liquidation address; a deposit is an
	// activity of a virtual account. Neither is a transfer.
	if tx.LiquidationAddressID != nil {
		return r.refreshDrain(ctx, tx)
	}
	if tx.VirtualAccountID != nil {
		return r.refreshDeposit(ctx, routes, tx)
	}

	var adapter ramp.ProviderAdapter
	var ref ramp.CustomerRef
	var err error
	if tx.FundedByAuthorization() {
		// A payout in flight is followed whatever has happened to the customer's
		// endorsement since: reading it moves no money, and gating it on
		// eligibility would leave funds in transit untracked.
		adapter, ref, err = routes.resolveSettlement(ctx, tx.CustomerID, tx.Provider)
	} else {
		adapter, ref, err = routes.resolve(ctx, tx.CustomerID, rail, tx.Provider)
	}
	if err != nil {
		return false, err
	}
	var order *ramp.RampOrder
	if tx.FundedByAuthorization() {
		order, err = r.readBoundPayout(ctx, adapter, ref, tx)
	} else {
		order, err = adapter.GetOrder(ctx, ref, tx.GatewayOrderID)
	}
	if err != nil {
		return false, err
	}
	previous := tx.Status
	// A payout under refund review keeps its status against a stale read; the
	// rest of the read is still taken.
	if models.RegressesStatus(previous, order.Status) {
		kept := *order
		kept.Status = ""
		order = &kept
	}
	if !OrderChangedFromProvider(tx, order) {
		return false, nil
	}
	if models.IsTerminalStatus(previous) && !models.CanLeaveTerminal(previous, order.Status) {
		return false, nil
	}

	ApplyOrder(tx, order)
	if order.Status == models.TxStatusCompleted && tx.CompletedAt == nil {
		now := time.Now()
		tx.CompletedAt = &now
	}
	updated, err := r.txRepo.SaveProviderSnapshotIfStatus(tx, previous)
	if err != nil {
		return false, fmt.Errorf("ramp reconcile: persist refresh: %w", err)
	}
	// A concurrent writer (webhook, sweep) moved the row between the provider
	// read and the write; treat as not changed from this call's perspective.
	return updated, nil
}

// readBoundPayout reads a bank payout against the binding stored at finalize.
// The generic GetOrder is unbound and refuses these ids, so there is no
// fall-back: a provider without the port, or a payout with no stored binding,
// is not read. A payout that is not the bound one is flagged for an operator,
// with ids only, and answers errPayoutAttention.
func (r *Reconciler) readBoundPayout(
	ctx context.Context,
	adapter ramp.ProviderAdapter,
	ref ramp.CustomerRef,
	tx *models.Transaction,
) (*ramp.RampOrder, error) {
	reader, ok := adapter.(ramp.BoundOrderReader)
	if !ok {
		return nil, fmt.Errorf("ramp reconcile: provider %s cannot read a bound payout: %w",
			tx.Provider, ramp.ErrProviderNotImplemented)
	}
	if r.bindings == nil {
		return nil, fmt.Errorf("ramp reconcile: no binding source for payout %s", tx.GatewayOrderID)
	}
	binding, err := r.bindings.PayoutBinding(tx.ID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		facades.Log().Errorf("ramp.attention: payout %s has no stored binding, not refreshed", tx.GatewayOrderID)
		return nil, errPayoutAttention
	}
	order, err := reader.GetBoundOrder(ctx, ref, tx.GatewayOrderID, *binding)
	if errors.Is(err, ramp.ErrBindingMismatch) {
		facades.Log().Errorf("ramp.attention: payout %s does not match the stored binding, not applied", tx.GatewayOrderID)
		r.markBindingMismatch(tx)
		return nil, errPayoutAttention
	}
	if err == nil {
		r.clearBindingMismatch(tx)
	}
	return order, err
}

// markBindingMismatch raises the attention mark the admin queue reads. The
// repository writes it once per code, so a payout that keeps diverging does not
// rewrite it every sweep. A failure is logged and never fails the read: the
// sweep goes on, and the next diverging read marks again.
func (r *Reconciler) markBindingMismatch(tx *models.Transaction) {
	if _, err := r.txRepo.MarkAttention(tx.ID, models.AttentionBindingMismatch, time.Now()); err != nil {
		facades.Log().Errorf("ramp.attention: mark payout %s as not matching its binding: %v", tx.GatewayOrderID, err)
	}
}

// clearBindingMismatch drops the mark once the provider's read matches the
// binding again. A failure is logged: a stale mark is cleared by the next clean
// read.
func (r *Reconciler) clearBindingMismatch(tx *models.Transaction) {
	if _, err := r.txRepo.ClearAttention(tx.ID); err != nil {
		facades.Log().Errorf("ramp.attention: clear the mark of payout %s: %v", tx.GatewayOrderID, err)
	}
}

// refreshDeposit re-reads a standing-account deposit out of the account's
// history.
//
// GetOrder would ask for `/transfers/{id}` and a deposit id is not a transfer
// id, so the transfer path does not merely return the wrong answer here — it
// 404s on every standing-account order the sweep ever scans, which is the whole
// on-ramp once this rail is live.
//
// Adapter, customer ref and account row come from the same routeCache the
// transfer path uses, memoized for the sweep. The adapter is *not* resolved
// through the router: re-reading a deposit is bookkeeping, and gating it on
// eligibility would freeze exactly the orders this sweep exists to unstick
// (see depositAdapter). Without the cache, N deposits into one account would
// re-query the account and the integration N times.
func (r *Reconciler) refreshDeposit(
	ctx context.Context,
	routes *routeCache,
	tx *models.Transaction,
) (bool, error) {
	account, err := routes.account(*tx.VirtualAccountID)
	if err != nil {
		return false, err
	}
	if account == nil {
		// The row points at an account that is gone. Nothing can be asked about
		// this deposit, and the operator has to see it rather than have the sweep
		// pass over it every run in silence.
		return false, fmt.Errorf("ramp reconcile: order %s names missing virtual account %d",
			tx.ExternalID, *tx.VirtualAccountID)
	}
	standing, ref, err := routes.depositAdapter(tx)
	if err != nil {
		return false, err
	}

	// The deposit id is what identifies the order on this rail, and it is in
	// GatewayOrderID as well as in its own column.
	deposit, err := standing.GetDeposit(ctx, ref, account.ExternalAccountID, tx.GatewayOrderID)
	if err != nil {
		return false, err
	}
	if deposit == nil {
		return false, nil
	}

	previous := tx.Status
	if !DepositChangedFromProvider(tx, account, deposit) {
		return false, nil
	}
	if models.IsTerminalStatus(previous) && !models.CanLeaveTerminal(previous, deposit.Status) {
		return false, nil
	}

	// The same fold the deposit webhook applies, deliberately shared: two copies
	// is how the sweep and the event stream end up disagreeing about one deposit,
	// and the fiat currency — which no activity carries — comes off the account
	// row in both.
	ApplyDeposit(tx, account, deposit)
	updated, err := r.txRepo.SaveProviderSnapshotIfStatus(tx, previous)
	if err != nil {
		return false, fmt.Errorf("ramp reconcile: persist deposit refresh: %w", err)
	}
	return updated, nil
}
