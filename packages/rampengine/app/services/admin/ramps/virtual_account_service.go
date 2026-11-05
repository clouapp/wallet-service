// Package ramps is the admin's view of a user's standing deposit accounts:
// what they have, where it delivers, what it charges, and the writes an
// operator can make against them — deactivation of a live account, and
// release of a stuck provisioning reservation.
package ramps

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

var (
	// ErrVirtualAccountNotFound is a read or a write for an account that does
	// not exist, or does not belong to the user in the path. The two are
	// deliberately one error: answering "not yours" would make another user's
	// accounts enumerable through a path an operator already has.
	ErrVirtualAccountNotFound = errors.New("admin ramps: virtual account not found")
	// ErrNoProviderCustomer means the user has no customer at the account's
	// provider to act for.
	ErrNoProviderCustomer = errors.New("admin ramps: no provider customer for the account")
	// ErrStandingAccountUnsupported is the provider's, aliased so a caller maps
	// this service's errors only.
	ErrStandingAccountUnsupported = ramp.ErrStandingAccountUnsupported
)

// Service drives the admin standing-account surface.
//
// The provider adapter comes from the factory and the customer id off the
// integration row — never through the router. Deactivation is the containment
// tool, and the compliance lapse that motivates it is the same state the router
// refuses on: routing this would break the action exactly when it is needed.
// The sweep and the webhook path reach the same conclusion for the same reason.
type Service struct {
	factory   *ramp.Factory
	integRepo repositories.CustomerProviderIntegrationRepository
	accounts  repositories.CustomerVirtualAccountRepository
	audit     *audit.Service
}

// NewVirtualAccountService wires the admin standing-account service.
func NewVirtualAccountService(
	factory *ramp.Factory,
	integRepo repositories.CustomerProviderIntegrationRepository,
	accounts repositories.CustomerVirtualAccountRepository,
	trail *audit.Service,
) *Service {
	return &Service{factory: factory, integRepo: integRepo, accounts: accounts, audit: trail}
}

// ListUserAccounts returns every standing account the user's individual
// customer holds — its id is the user's — deactivated ones included. The
// deactivated account is often the one under investigation: it is where the
// money went, and it is history by the time anybody asks.
func (s *Service) ListUserAccounts(userID uint) ([]models.CustomerVirtualAccount, error) {
	return s.accounts.FindByCustomer(userID)
}

// Deactivate stops an account from collecting.
//
// Funds sent to a deactivated account are returned to the sender, which is what
// makes this containment rather than deletion: the account and its history stay,
// and the deposit code the user saved simply stops working. The provider is
// asked first and its answer is what gets persisted — a row saying `inactive`
// over an account still collecting would be a lie in the direction that costs
// money.
//
// A stuck `provisioning` reservation is different: nothing exists upstream to
// turn off, and leaving the row blocks the user forever (ErrProvisioningInFlight
// on every retry). Soft-deleting it is the operator lever that frees the slot.
//
// Calling it on an already-deactivated account is a no-op, not an error: the
// containment being asked for is already in force.
func (s *Service) Deactivate(
	ctx context.Context, actor auditdto.ActorDTO, userID, accountID uint,
) (*models.CustomerVirtualAccount, error) {
	account, err := s.accounts.FindByID(accountID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: load virtual account: %w", err)
	}
	// The user's own accounts are their individual customer's, whose id is
	// theirs; a company's account is not the user's even when they provisioned
	// it.
	if account == nil || account.CustomerID != userID {
		return nil, ErrVirtualAccountNotFound
	}
	if account.Status == models.VirtualAccountStatusProvisioning {
		return s.releaseReservation(ctx, actor, account)
	}
	return s.deactivateLive(ctx, actor, account)
}

// releaseReservation clears a reservation whose provider call never
// completed: there is nothing upstream to deactivate, and clearing the row is
// what unblocks the user. The row is deleted only while it is still a
// reservation. One the client finalized since it was read is a live deposit
// code, deactivated like any other; one a second click already released is
// answered as released, recorded once.
func (s *Service) releaseReservation(
	ctx context.Context, actor auditdto.ActorDTO, account *models.CustomerVirtualAccount,
) (*models.CustomerVirtualAccount, error) {
	err := s.accounts.ReleaseReservation(account, audit.Entry(actor, deactivation(account)))
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, repositories.ErrNotProvisioning) {
		return nil, fmt.Errorf("admin ramps: release stuck reservation: %w", err)
	}
	current, err := s.accounts.FindByID(account.ID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: reload virtual account: %w", err)
	}
	if current == nil {
		return account, nil
	}
	return s.deactivateLive(ctx, actor, current)
}

// deactivateLive turns off an account the provider holds, and answers an
// inactive one as it is.
func (s *Service) deactivateLive(
	ctx context.Context, actor auditdto.ActorDTO, account *models.CustomerVirtualAccount,
) (*models.CustomerVirtualAccount, error) {
	if !account.IsActive() {
		return account, nil
	}

	standing, ref, err := s.adapterFor(account)
	if err != nil {
		return nil, err
	}
	inactive := false
	updated, err := standing.UpdateVirtualAccount(ctx, ref, account.ExternalAccountID,
		ramp.VirtualAccountUpdate{Active: &inactive})
	if err != nil {
		return nil, fmt.Errorf("admin ramps: deactivate virtual account: %w", err)
	}

	// The local status is set first and the provider's echo folded over it, so a
	// response that names no status still records the deactivation we performed
	// — and a provider that refused it, and echoes `active`, overwrites us back
	// to the truth.
	account.Status = models.VirtualAccountStatusInactive
	rampsvc.MergeVirtualAccount(account, updated)
	if err := s.accounts.Update(account); err != nil {
		return nil, fmt.Errorf("admin ramps: persist virtual account: %w", err)
	}
	// After the provider call: it cannot be rolled back, so a failed insert is
	// logged and the deactivation stands.
	s.audit.RecordIrreversible(actor, deactivation(account))
	return account, nil
}

// deactivation is the audit entry of an operator deactivating account.
func deactivation(account *models.CustomerVirtualAccount) auditdto.EntryDTO {
	return auditdto.EntryDTO{
		Action: audit.ActionVirtualAccountDeactivated, SubjectType: audit.SubjectCustomers, SubjectID: account.CustomerID,
		Provider: account.Provider, Metadata: map[string]any{"virtual_account_id": account.ID},
	}
}

// adapterFor resolves the standing-account port and the customer to act for.
func (s *Service) adapterFor(
	account *models.CustomerVirtualAccount,
) (ramp.VirtualAccountAdapter, ramp.CustomerRef, error) {
	adapter, err := s.factory.Get(account.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("admin ramps: resolve %s: %w", account.Provider, err)
	}
	standing, err := ramp.StandingAccount(adapter)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("admin ramps: %w", err)
	}

	integration, err := s.integRepo.FindByCustomerAndProvider(account.CustomerID, account.Provider)
	if err != nil {
		return nil, ramp.CustomerRef{}, fmt.Errorf("admin ramps: load integration: %w", err)
	}
	if integration == nil || integration.ExternalCustomerID == nil || *integration.ExternalCustomerID == "" {
		return nil, ramp.CustomerRef{}, fmt.Errorf("%w: %s, account %d", ErrNoProviderCustomer, account.Provider, account.ID)
	}
	// The integration's status is deliberately not checked: see the note on
	// Service. Containment must work when the user's compliance state is bad.
	return standing, ramp.NewCustomerRef(account.CustomerID, integration.ExternalCustomerID), nil
}
