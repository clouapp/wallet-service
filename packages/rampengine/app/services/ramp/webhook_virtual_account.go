package ramp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// accountView is what the pre-transaction read produced: the local row the
// event names, and what the provider says that account now is.
type accountView struct {
	row     *models.CustomerVirtualAccount
	current *ramp.VirtualAccount
}

// prefetchAccountView asks the provider what an account now is, when the event
// is an account-level change. Other kinds return nil: they need no upstream
// round-trip before the short persist transaction.
func (p *WebhookProcessor) prefetchAccountView(
	ctx context.Context,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
) (*accountView, error) {
	if result.Kind != ramp.WebhookKindVirtualAccount {
		return nil, nil
	}
	accountID := result.ProviderAccountID
	if accountID == "" && result.Account != nil {
		accountID = result.Account.ProviderAccountID
	}
	account, err := p.virtualAccount(event.Provider, accountID)
	if err != nil {
		return nil, err
	}
	current, err := p.readVirtualAccount(ctx, account, result.ExternalCustomerID)
	if err != nil {
		return nil, err
	}
	return &accountView{row: account, current: current}, nil
}

// applyVirtualAccount records an account-level change. It updates the account
// row and never a transaction.
//
// The event is a trigger, not a snapshot. An activity carries no deposit
// instructions and no source currency, so every empty field on result.Account
// means "this event says nothing about it", never "clear it" — and `status` is
// deliberately empty on an account_update, where asserting `active` would
// revive an account somebody deactivated on purpose. Rather than rebuild the
// new state out of the event's before/after pairs, Process re-reads the account
// before opening this transaction: account changes are rare, and one call buys
// certainty over an inference without holding Postgres across the upstream
// round-trip.
func (p *WebhookProcessor) applyVirtualAccount(
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	view *accountView,
) (func(), error) {
	if view == nil || view.current == nil {
		// The provider answered no account for one it had just sent an event
		// about. VirtualAccountSyncer.syncOne treats that as a row to skip,
		// because a sweep walks accounts nobody asked about; here an event
		// named this one, so it belongs in processing_error.
		return nil, fmt.Errorf("ramp webhook: %s read back no virtual account", event.Provider)
	}
	// view.row was read before the transaction, to ask the provider about it;
	// the write goes onto the row as it stands under the lock.
	account, err := db.FindVirtualAccountForUpdate(view.row.ID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: lock virtual account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("ramp webhook: virtual account %d is gone", view.row.ID)
	}

	MergeVirtualAccount(account, view.current)
	synced := time.Now()
	account.LastSyncedAt = &synced

	if err := db.UpdateVirtualAccount(account); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist virtual account: %w", err)
	}
	// Nothing is broadcast. A standing account is not an order: the app reads it
	// when the user opens the deposit screen, and the SSE stream carries money
	// movement.
	return nil, nil
}

// virtualAccount resolves the provider's account id back to the local row.
//
// A miss is an error rather than silence, and deliberately so: money arriving
// in an account we do not know is an operational anomaly, and it belongs in
// `processing_error` where an operator finds it.
func (p *WebhookProcessor) virtualAccount(provider, externalAccountID string) (*models.CustomerVirtualAccount, error) {
	if externalAccountID == "" {
		return nil, fmt.Errorf("ramp webhook: %s event names no virtual account", provider)
	}
	account, err := p.accounts.FindByProviderAndExternalID(provider, externalAccountID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load virtual account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("ramp webhook: no %s virtual account %s", provider, externalAccountID)
	}
	return account, nil
}

// readVirtualAccount asks the provider what the account now is.
//
// The adapter comes from the factory, not the router, and that is deliberate.
// The router answers "may this user transact on this rail", which is a question
// about eligibility — an inactive integration or a lapsed endorsement makes it
// refuse. Reading an account back is bookkeeping, not transacting, and gating it
// on eligibility breaks it exactly when it matters: a `deactivation` arriving
// because the user's compliance state lapsed would be refused by the same lapse,
// leaving the row saying `active` and the app handing out deposit instructions
// for an account whose funds are returned to the sender.
//
// The customer id comes off the event, which is the same source applyKyc trusts
// to find a user without consulting the router.
func (p *WebhookProcessor) readVirtualAccount(
	ctx context.Context,
	account *models.CustomerVirtualAccount,
	externalCustomerID string,
) (*ramp.VirtualAccount, error) {
	if externalCustomerID == "" {
		return nil, fmt.Errorf("ramp webhook: %s account event carries no customer id", account.Provider)
	}
	standing, err := virtualAccountAdapter(p.factory, account.Provider)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: %w", err)
	}
	ref := ramp.CustomerRef{CustomerID: account.CustomerID, ExternalID: externalCustomerID}
	current, err := standing.GetVirtualAccount(ctx, ref, account.ExternalAccountID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: read virtual account: %w", err)
	}
	return current, nil
}

// MergeVirtualAccount folds the provider's read onto the row.
//
// Even a full read only writes what it reports. A field the provider did not
// answer is not an instruction to blank a not-null column, and the destination
// and deposit instructions are what the user's money depends on.
func MergeVirtualAccount(row *models.CustomerVirtualAccount, current *ramp.VirtualAccount) {
	if current == nil {
		return
	}
	if current.Status != "" {
		row.Status = current.Status
	}
	if current.Destination.Currency != "" {
		row.DestinationCurrency = current.Destination.Currency
	}
	if current.Destination.PaymentRail != "" {
		row.DestinationChain = current.Destination.PaymentRail
	}
	if current.Destination.Address != "" {
		row.DestinationAddress = current.Destination.Address
	}
	if current.FeePercent != "" {
		row.FeePercent = &current.FeePercent
	}
	if len(current.DepositInstructions) > 0 {
		if encoded, err := json.Marshal(current.DepositInstructions); err == nil {
			row.DepositInstructions = string(encoded)
		}
	}
	if len(current.Raw) > 0 {
		row.RawPayload = string(current.Raw)
	}
}
