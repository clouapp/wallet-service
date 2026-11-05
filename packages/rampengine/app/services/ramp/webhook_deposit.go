package ramp

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// depositRailFor is the rail a standing-account deposit settles on: the one
// that collects the account's currency (pix_payin for BRL, usd_payin for USD).
// The currency is the account row's `source_currency`; an account in a
// currency no rail collects is an error, never a default.
func depositRailFor(account *models.CustomerVirtualAccount) (ramp.Rail, error) {
	rail, ok := ramp.PayinRailFor(account.SourceCurrency)
	if !ok {
		return "", fmt.Errorf("ramp webhook: virtual account %d collects %q, which no rail serves",
			account.ID, account.SourceCurrency)
	}
	return rail, nil
}

// applyVirtualAccountDeposit creates or moves the transaction a deposit
// describes.
//
// This is the one branch where the event *creates* the row, and applyRamp's
// refusal of a missing transaction is the exact inverse. There, a miss means
// our own create is still in flight. Here there is no create at all: the
// deposit code is permanent and carries no amount, so the first activity Bridge
// sends is the first XIP hears of the money.
//
// Everything is read from result.Deposit and nothing from the top level. A
// virtual-account deposit sets no top-level Status, TxHash or ProviderOrderID,
// so reaching for the fields applyRamp uses would silently read a zero value.
func (p *WebhookProcessor) applyVirtualAccountDeposit(
	ctx context.Context,
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
) (func(), error) {
	deposit := result.Deposit
	if deposit == nil || deposit.DepositID == "" {
		// Authentic, but with no identity there is nothing to key a row on and
		// no way to recognize the next delivery as the same money.
		return nil, nil
	}

	accountID := result.ProviderAccountID
	if accountID == "" {
		accountID = deposit.ProviderAccountID
	}
	account, err := p.virtualAccount(event.Provider, accountID)
	if err != nil {
		return nil, err
	}

	tx, err := db.FindDepositForUpdate(event.Provider, deposit.DepositID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load deposit transaction: %w", err)
	}
	if tx == nil {
		return p.createDepositTransaction(ctx, db, event, account, deposit)
	}

	// Delivery is out of order and duplicated by default on this rail, and the
	// terminal guard is what makes that safe: a funds_received arriving after
	// the payment event that already settled the deposit must not walk it back.
	if models.IsTerminalStatus(tx.Status) && !models.CanLeaveTerminal(tx.Status, deposit.Status) {
		return nil, nil
	}

	ApplyDeposit(tx, account, deposit)
	if err := db.UpdateTransaction(tx); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist deposit transaction: %w", err)
	}
	event.TransactionID = &tx.ID
	return func() { p.announce(tx) }, nil
}

// createDepositTransaction turns the first activity of a deposit into the order.
func (p *WebhookProcessor) createDepositTransaction(
	ctx context.Context,
	db repositories.WebhookTx,
	event *models.WebhookEvent,
	account *models.CustomerVirtualAccount,
	deposit *ramp.VirtualAccountDeposit,
) (func(), error) {
	// A closed payin rail stops the standing account from opening new orders.
	//
	// The event is accepted rather than refused: the deposit already landed in
	// the account, the operator who closed the rail is the one who decides what
	// happens to it, and refusing the callback would only make the provider
	// redeliver it forever. Returning (nil, nil) leaves the caller to stamp the
	// event processed with nothing to broadcast.
	//
	// This is the reason the veto lives at named call sites instead of in the
	// router: there is no request, no authenticated user and no route here for
	// a middleware to hang on. The flags are the member's who provisioned the
	// account, as a request's are its user's (spec §5 F2).
	depositRail, err := depositRailFor(account)
	if err != nil {
		return nil, err
	}
	on, err := p.features.IsActive(ctx, depositRail.Flag(), models.UserFlagScope(account.UserID))
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: %s switch for user %d: %w", depositRail, account.UserID, err)
	}
	if !on {
		facades.Log().Warningf(
			"ramp webhook: payin rail closed for user %d — deposit %s recorded, no transaction opened",
			account.UserID, deposit.DepositID)
		return nil, nil
	}

	rail := depositRail.String()
	depositID := deposit.DepositID
	tx := &models.Transaction{
		// The order is the account's customer's; who acted is the member who
		// provisioned the account (spec §18.6).
		CustomerID: account.CustomerID,
		UserID:     account.UserID,
		Provider:   event.Provider,
		Type:       models.TransactionTypeOnramp,
		Rail:       &rail,
		// The account's currency is the order's: BRL for Pix, USD for a bank
		// deposit.
		FiatCurrency: account.SourceCurrency,
		// The deposit id is the whole identity of an order here. There is no
		// client create, so neither an idempotency key nor an estimate applies,
		// and the partial unique on (provider, deposit_id) is what makes two
		// concurrent deliveries safe.
		ExternalID:     uuid.NewString(),
		GatewayOrderID: depositID,
		DepositID:      &depositID,
		// The token vocabulary the client reads is uppercase; the account row
		// keeps the provider's lowercase echo of the same token.
		TargetToken:       strings.ToUpper(account.DestinationCurrency),
		DestinationWallet: account.DestinationAddress,
		VirtualAccountID:  &account.ID,
		FeePercent:        account.FeePercent,
		// ExpiresAt stays nil: the money arrived before the row existed, so
		// there is no payment window to miss and Reconciler.expire — which only
		// acts on ExpiresAt != nil — leaves it alone.
		//
		// The payload carries the payer's data. Persisted, never logged.
		RawPayload: rawPayload(event.RawPayload),
	}
	ApplyDeposit(tx, account, deposit)

	if err := db.CreateTransaction(tx); err != nil {
		// A concurrent duplicate delivery loses this insert to
		// `uniq_tx_provider_deposit`. That is the designed path, not a failure:
		// the winner's row is the one that counts, and Reconciler.refreshDeposit
		// re-reads the deposit history to settle it.
		//
		// `webhooks:reprocess` does NOT cover this: stamp() sets ProcessedAt even
		// on failure and FindUnprocessed filters `processed_at IS NULL`. What
		// retries is the queue itself, then failed_jobs.
		return nil, fmt.Errorf("ramp webhook: create deposit transaction: %w", err)
	}
	event.TransactionID = &tx.ID
	return func() { p.announce(tx) }, nil
}

// rawPayload keeps the event body for the jsonb column, which will not take an
// empty string.
func rawPayload(body string) string {
	if body == "" {
		return "{}"
	}
	return body
}
