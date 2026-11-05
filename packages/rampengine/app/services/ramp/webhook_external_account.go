package ramp

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// KycRefresher re-reads one user's compliance state from a provider and
// stores it — kycsvc.ProviderKycService.Refresh. An interface so this package
// does not import the KYC service, and so a test can stand one in.
type KycRefresher interface {
	Refresh(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderKyc, error)
}

// WithKycRefresher sets what an external account event re-syncs the user's
// KYC row through. Without one, those events are recorded and acknowledged
// and nothing more, as before.
func (p *WebhookProcessor) WithKycRefresher(refresher KycRefresher) *WebhookProcessor {
	p.kycRefresher = refresher
	return p
}

// applyExternalAccount answers a change to the user's registered bank account
// with a re-read of their KYC row. Bridge lists `external_account` in the
// customer's requirements_due until one exists, and sends no customer event
// when it clears, so without this the requirement stays on the row — and on
// the admin's "what is missing" — until something else happens to re-sync it.
//
// The refresh is a provider call, so it runs after the commit, off the event
// lock, and its failure is logged rather than returned: the event itself was
// applied (there is nothing of its own to write), and failing it would only
// make the provider retry a delivery that is not the problem. A refresh is a
// re-read, so running it again on a duplicate delivery or a reprocess is
// harmless.
func (p *WebhookProcessor) applyExternalAccount(
	ctx context.Context, event *models.WebhookEvent, result *ramp.WebhookResult,
) (func(), error) {
	if p.kycRefresher == nil || result.ExternalCustomerID == "" {
		return nil, nil
	}
	provider, customerID := event.Provider, result.ExternalCustomerID
	return func() { p.refreshKycFor(ctx, provider, customerID) }, nil
}

// refreshKycFor resolves the provider-side customer to the user and refreshes
// their row, logging whatever stops it.
func (p *WebhookProcessor) refreshKycFor(ctx context.Context, provider, customerID string) {
	link, err := p.integRepo.FindByExternalCustomerID(provider, customerID)
	if err != nil {
		facades.Log().WithContext(ctx).Warningf(
			"ramp webhook: external account: load %s integration: %v", provider, err)
		return
	}
	if link == nil {
		facades.Log().WithContext(ctx).Warningf(
			"ramp webhook: external account: no integration for %s customer", provider)
		return
	}
	if _, err := p.kycRefresher.Refresh(ctx, link.CustomerID, provider); err != nil {
		facades.Log().WithContext(ctx).Warningf(
			"ramp webhook: external account: refresh %s kyc for customer %d: %v", provider, link.CustomerID, err)
	}
}
