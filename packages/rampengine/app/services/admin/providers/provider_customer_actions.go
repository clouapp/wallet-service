package providers

import (
	"context"
	"errors"
	"fmt"

	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// ErrKycNotFound is a reset of a provider the customer holds no KYC row at.
var ErrKycNotFound = kycsvc.ErrKycNotFound

// ErrIntegrationNotFound is an unlink of a provider the user is not linked to.
var ErrIntegrationNotFound = errors.New("admin providers: integration not found")

// ErrVirtualAccountActive refuses an unlink while the user holds a standing
// account at the provider that is active or being provisioned: money can
// still arrive there, and its webhook would find no link.
var ErrVirtualAccountActive = repositories.ErrVirtualAccountOpen

// ErrTransactionOpen refuses an unlink while a transaction at the provider
// has not reached a terminal status.
var ErrTransactionOpen = repositories.ErrTransactionOpen

// ResetKyc clears XIP's verdict on one provider and keeps the customer
// link and the terms (kycsvc.ResetVerdict). The reset and its kyc.reset row,
// which keeps the previous status and categories, are written in one
// transaction; the linked customer is read back after it commits.
func (s *Service) ResetKyc(
	ctx context.Context, actor auditdto.ActorDTO, customerID uint, provider string,
) (*models.CustomerProviderKyc, error) {
	reset, err := s.kyc.ResetVerdict(ctx, customerID, provider, func(previous *models.CustomerProviderKyc) *models.AdminAuditLog {
		categories := previous.DecodedRejectionCategories()
		if categories == nil {
			categories = []string{}
		}
		return audit.Entry(actor, auditdto.EntryDTO{
			Action: audit.ActionKycReset, SubjectType: audit.SubjectCustomers, SubjectID: customerID, Provider: provider,
			Metadata: map[string]any{"previous_status": previous.KycStatus, "previous_categories": categories},
		})
	})
	if err != nil {
		return nil, err
	}
	return reset.Row, nil
}

// UnlinkIntegration forgets the customer's provider customer at provider: the link and
// the KYC row are soft-deleted (the row cleared first, under the submit lock)
// and the payout destinations registered there deleted, with the audit row,
// in one transaction.
//
// It is refused while money is in flight at the provider — an active or
// provisioning standing account, a transaction not yet terminal — or while a
// submission is on its way there. It does not delete the customer upstream:
// on Bridge the next application adopts the same customer by e-mail, so this
// is how a customer is moved to another account or stale state dropped, not
// how a fresh verification starts.
func (s *Service) UnlinkIntegration(ctx context.Context, actor auditdto.ActorDTO, customerID uint, provider string) error {
	link, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return fmt.Errorf("admin providers: unlink: load integration: %w", err)
	}
	if link == nil {
		return ErrIntegrationNotFound
	}
	// Refused early, before a lock is taken; the transaction checks again.
	if err := s.refuseMoneyInFlight(customerID, provider); err != nil {
		return err
	}
	// The provision lock keeps a standing account from being reserved between
	// the transaction's money check and the unlink: provisioning takes it for
	// its reservation. An order placed in between is not held off — orders are
	// tracked by their own id, not through the link.
	err = s.locker.WithLock(ctx, clientramps.ProvisionLockKey(customerID), func(ctx context.Context) error {
		return s.kyc.ForgetVerdict(ctx, customerID, provider, func(row *models.CustomerProviderKyc) error {
			return s.integRepo.Unlink(customerID, provider, row, func(locked *models.CustomerProviderIntegration) *models.AdminAuditLog {
				return unlinkEntry(actor, locked)
			})
		})
	})
	// A second unlink, or one racing the first, finds no live link.
	if errors.Is(err, repositories.ErrIntegrationGone) {
		return ErrIntegrationNotFound
	}
	return err
}

// unlinkEntry records the unlink of the link the transaction deleted, with
// the customer id it held.
func unlinkEntry(actor auditdto.ActorDTO, link *models.CustomerProviderIntegration) *models.AdminAuditLog {
	customer := ""
	if link.ExternalCustomerID != nil {
		customer = *link.ExternalCustomerID
	}
	return audit.Entry(actor, auditdto.EntryDTO{
		Action: audit.ActionIntegrationUnlinked, SubjectType: audit.SubjectCustomers, SubjectID: link.CustomerID,
		Provider: link.Provider, Metadata: map[string]any{"external_customer_id": customer},
	})
}

// refuseMoneyInFlight answers why an unlink would strand money at provider,
// or nil.
func (s *Service) refuseMoneyInFlight(userID uint, provider string) error {
	open, err := s.accounts.HasOpenByCustomerAndProvider(userID, provider)
	if err != nil {
		return fmt.Errorf("admin providers: unlink: read virtual accounts: %w", err)
	}
	if open {
		return ErrVirtualAccountActive
	}
	open, err = s.transactions.HasOpenByCustomerAndProvider(userID, provider)
	if err != nil {
		return fmt.Errorf("admin providers: unlink: read transactions: %w", err)
	}
	if open {
		return ErrTransactionOpen
	}
	return nil
}
