package ramp

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

type walletCustomerResolver interface {
	CustomerIDForWallet(ctx context.Context, walletID string) (string, error)
}

// prefetchDepositOwner fills in the customer behind a managed-wallet deposit
// that names only its wallet. It asks the provider, so it runs before the event
// transaction opens, the way prefetchAccountView does.
func (p *WebhookProcessor) prefetchDepositOwner(ctx context.Context, event *models.WebhookEvent, result *ramp.WebhookResult) error {
	if result.Kind != ramp.WebhookKindManagedWalletDeposit || result.ManagedWalletDeposit == nil ||
		event.Provider != models.ProviderOzav || result.ExternalCustomerID != "" {
		return nil
	}
	walletID := result.ManagedWalletDeposit.WalletID
	if walletID == "" {
		walletID = result.ProviderAccountID
	}
	if walletID == "" {
		return errors.New("ramp webhook: deposit event missing wallet id")
	}
	adapter, err := p.factory.Get(ramp.ProviderOzav)
	if err != nil {
		return fmt.Errorf("ramp webhook: resolve ozav adapter: %w", err)
	}
	resolver, ok := adapter.(walletCustomerResolver)
	if !ok {
		return errors.New("ramp webhook: ozav adapter cannot resolve wallet owner")
	}
	customerID, err := resolver.CustomerIDForWallet(ctx, walletID)
	if err != nil {
		return fmt.Errorf("ramp webhook: resolve deposit wallet owner: %w", err)
	}
	result.ExternalCustomerID = customerID
	return nil
}

// applyManagedWalletDeposit hands a deposit to the off-ramp service. The claim
// it makes is idempotent on the deposit's hash, so a rollback of this event
// followed by a reprocess claims nothing twice; the settle enqueue it returns
// runs after the commit, with the broadcasts.
func (p *WebhookProcessor) applyManagedWalletDeposit(
	ctx context.Context,
	event *models.WebhookEvent,
	result *ramp.WebhookResult,
) (func(), error) {
	if p.settler == nil || result.ManagedWalletDeposit == nil || event.Provider != models.ProviderOzav {
		return nil, nil
	}
	if result.ExternalCustomerID == "" {
		return nil, errors.New("ramp webhook: deposit owner was not resolved")
	}
	integ, err := p.integRepo.FindByExternalCustomerID(event.Provider, result.ExternalCustomerID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: resolve deposit customer: %w", err)
	}
	if integ == nil {
		facades.Log().WithContext(ctx).With(map[string]any{
			"customer_id": result.ExternalCustomerID,
		}).Warning("ramp webhook: deposit.credited for unknown customer")
		return nil, nil
	}
	enqueue, err := p.settler.ApplyManagedWalletDeposit(ctx, integ.CustomerID, *result.ManagedWalletDeposit)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: apply managed wallet deposit: %w", err)
	}
	return enqueue, nil
}
