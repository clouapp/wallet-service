package ramp

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
)

// applyKyc moves a customer's compliance state.
//
// The event carries the provider's customer id, not ours, which is exactly
// what `customer_provider_integrations` is indexed for.
func (p *WebhookProcessor) applyKyc(db repositories.WebhookTx, event *models.WebhookEvent, result *ramp.WebhookResult) (func(), error) {
	if result.ExternalCustomerID == "" {
		return nil, nil
	}
	link, err := p.integRepo.FindByExternalCustomerID(event.Provider, result.ExternalCustomerID)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load integration: %w", err)
	}
	if link == nil {
		return nil, p.unlinkedCustomer(event, result.ExternalCustomerID)
	}
	if result.IntegrationDisabled {
		return nil, p.disableIntegration(db, event, link)
	}

	row, err := db.FindKycForUpdate(link.CustomerID, event.Provider)
	if err != nil {
		return nil, fmt.Errorf("ramp webhook: load kyc: %w", err)
	}
	if row == nil {
		return nil, fmt.Errorf("ramp webhook: no kyc row for %s customer", event.Provider)
	}

	previous := row.Posture()

	if result.Status != "" {
		row.KycStatus = result.Status
		// The categories are part of the posture the status states: a
		// rejection brings its reasons, anything else clears the last ones.
		// An event without a status (a verification session's) says nothing
		// about either and leaves both as they were.
		row.SetRejectionCategories(result.RejectionCategories)
		row.SetRejectionDetails(result.RejectionDetails)
	}
	if len(result.Endorsements) > 0 {
		if err := row.SetEndorsements(result.Endorsements); err != nil {
			return nil, fmt.Errorf("ramp webhook: encode endorsements: %w", err)
		}
	}
	// The same two columns applyKycState writes on the sync path. They are what
	// pending_steps is derived from, so a webhook that moved them without
	// persisting them left the row — and therefore the client's wizard —
	// describing whatever the last poll had seen. Guarded, because a decoder
	// that does not read these fields reports nil for both, and writing that
	// would erase the sync's answer rather than replace it.
	if result.ReportsRequirements {
		row.SetRequirements(result.Requirements)
		row.SetRejectionReasons(result.RejectionReasons)
	}
	// The payload carries customer PII, so the row's raw_status is updated from
	// it but nothing about it is logged.
	if len(result.Raw) > 0 {
		row.RawStatus = string(result.Raw)
	}
	now := time.Now()
	row.LastSyncedAt = &now

	if err := db.UpdateKyc(row); err != nil {
		return nil, fmt.Errorf("ramp webhook: persist kyc: %w", err)
	}
	// An unchanged status is still not news — but "news" is what the screen
	// shows (models.KycPosture), requirements included. The client derives its
	// wizard from them, so a provider that holds the status and changes what
	// it wants must reach the app all the same.
	if row.Posture() == previous {
		return nil, nil
	}
	return func() {
		p.publishKyc(row)
		p.notifier.KycUpdated(previous.Status, row)
	}, nil
}

// unlinkedCustomer answers a KYC event about a customer no link names.
//
// One XIP detached to retry the customer with a new provider-side one (DetachCustomer)
// keeps sending events about itself, and those are acknowledged: nothing
// anyone can act on, and failing them would leave each stamped with an error.
// Ids only in the log: the payload carries the customer's PII.
//
// Any other customer is an error, as it always was. It may be one whose create
// has not committed its link yet, and an error stamp leaves the event
// replayable, where a clean one would make every replay a no-op and lose it.
func (p *WebhookProcessor) unlinkedCustomer(event *models.WebhookEvent, customerID string) error {
	detached, err := p.integRepo.IsDetachedCustomer(event.Provider, customerID)
	if err != nil {
		return fmt.Errorf("ramp webhook: read detached customers: %w", err)
	}
	if !detached {
		return fmt.Errorf("ramp webhook: no integration for %s customer", event.Provider)
	}
	facades.Log().Infof("ramp webhook: event %d: %s customer %s was detached; acknowledged",
		event.ID, event.Provider, customerID)
	return nil
}

// disableIntegration switches off the link of a customer the provider deleted,
// the state a Bridge refresh writes for an offboarded one: the router no
// longer routes through it (IsUsable), and the KYC row keeps what the last
// sync wrote. Ids only in the log.
func (p *WebhookProcessor) disableIntegration(
	db repositories.WebhookTx, event *models.WebhookEvent, link *models.CustomerProviderIntegration,
) error {
	if link.Status == models.IntegrationStatusDisabled {
		return nil
	}
	link.Status = models.IntegrationStatusDisabled
	if err := db.UpdateIntegration(link); err != nil {
		return fmt.Errorf("ramp webhook: disable integration: %w", err)
	}
	facades.Log().Infof("ramp webhook: event %d: %s deleted customer %s; integration of customer %d switched off",
		event.ID, event.Provider, *link.ExternalCustomerID, link.CustomerID)
	return nil
}

// publishKyc sends every user who acts for the row's customer the frame
// ProviderKycService.publish sends from the sync path, requirements included:
// they are one of the reasons it fired.
func (p *WebhookProcessor) publishKyc(row *models.CustomerProviderKyc) {
	p.audience.Kyc(broadcast.KycEvent{
		CustomerID:       row.CustomerID,
		Provider:         row.Provider,
		KycStatus:        row.KycStatus,
		TosStatus:        row.TosStatus,
		CollectionMode:   row.CollectionMode,
		HostedKycURL:     row.HostedKycURL,
		Requirements:     row.DecodedRequirements(),
		RejectionReasons: row.DecodedRejectionReasons(),
	})
}
