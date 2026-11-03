package kyc

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrKycNotFound is a reset of a provider the user holds no KYC row at.
var ErrKycNotFound = errors.New("kyc: no kyc row")

// VerdictReset is what ResetVerdict found and what it left.
type VerdictReset struct {
	PreviousStatus     string
	PreviousCategories []string
	// Row is the row after the reset, read back from the provider when a
	// customer is linked.
	Row *models.CustomerProviderKyc
}

// ResetVerdict clears XIP's verdict on one provider and keeps the customer
// link and the terms: the support action that gives a user back a provider
// after a terminal or local refusal (an Ozav create refused over a CPF stays
// rejected forever otherwise).
//
// It takes the submit lock and refuses what a second submission is refused
// (ErrSubmissionInFlight): a worker holding a staged payload would write its
// answer over the reset. The row is rewritten in place, never soft-deleted —
// every KYC writer does a full-row save, which on a soft-deleted row falls back
// to an upsert that resurrects it.
//
// With a customer linked the row is then refreshed, so it shows the provider's
// current truth; a provider still saying rejected brings its categories back,
// which is the answer. That refresh runs after the reset is committed, and a
// failure there leaves the reset standing: it is logged, and the operator
// refreshes again. Without a customer the row reads not_started and owes the
// application, and the next one creates or adopts a customer.
//
// record builds the audit row of the reset from the row as it was; the reset
// and that row are written in one transaction, before the refresh.
func (s *ProviderKycService) ResetVerdict(
	ctx context.Context, customerID uint, provider string, record func(previous *models.CustomerProviderKyc) *models.AdminAuditLog,
) (*VerdictReset, error) {
	result := &VerdictReset{}
	err := s.withClearedVerdict(ctx, customerID, provider, func(row *models.CustomerProviderKyc, previous *models.CustomerProviderKyc) error {
		if row == nil {
			return ErrKycNotFound
		}
		err := s.kycRepo.UpdateRecorded(row, record(previous))
		if errors.Is(err, repositories.ErrKycGone) {
			return ErrKycNotFound
		}
		if err != nil {
			return fmt.Errorf("kyc: reset: persist: %w", err)
		}
		result.PreviousStatus = previous.KycStatus
		result.PreviousCategories = previous.DecodedRejectionCategories()
		result.Row = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result.Row.KycStatus != result.PreviousStatus {
		s.publish(result.Row, "")
	}
	result.Row = s.afterReset(ctx, result.Row)
	return result, nil
}

// ForgetVerdict is the KYC half of unlinking a provider customer: it clears
// the user's row at provider as ResetVerdict does, under the same lock and
// refusing the same in-flight submission, and hands the cleared row — nil when
// there is none — to persist, which writes it (soft-deleted) together with the
// rest of the unlink. Cleared before it is deleted because every KYC writer
// saves the full row, and a stale save resurrects a soft-deleted one: what
// comes back then carries no verdict. persist runs inside the lock.
func (s *ProviderKycService) ForgetVerdict(
	ctx context.Context, customerID uint, provider string, persist func(*models.CustomerProviderKyc) error,
) error {
	return s.withClearedVerdict(ctx, customerID, provider, func(row, _ *models.CustomerProviderKyc) error {
		return persist(row)
	})
}

// withClearedVerdict runs fn under the submit lock with the user's row at
// provider cleared and a copy of it as it was, or (nil, nil) when there is
// none. A row a submission is still on its way with is refused
// (ErrSubmissionInFlight): the worker would write its answer over the clear.
func (s *ProviderKycService) withClearedVerdict(
	ctx context.Context, customerID uint, provider string, fn func(row, previous *models.CustomerProviderKyc) error,
) error {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return err
	}
	return s.locker.WithLock(ctx, lockKey("submit", customerID, provider), func(context.Context) error {
		row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
		if err != nil {
			return fmt.Errorf("kyc: lookup: %w", err)
		}
		if row == nil {
			return fn(nil, nil)
		}
		if err := s.guardInFlight(row, KindApplication, customerID, provider); err != nil {
			return err
		}
		previous := *row
		clearVerdict(row, adapter.PreferredKycMode().String())
		return fn(row, &previous)
	})
}

// afterReset reads a linked customer back from the provider, or marks the row
// as owing the application when there is none.
func (s *ProviderKycService) afterReset(ctx context.Context, row *models.CustomerProviderKyc) *models.CustomerProviderKyc {
	link, err := s.integRepo.FindByCustomerAndProvider(row.CustomerID, row.Provider)
	if err != nil {
		facades.Log().Errorf("kyc: reset: reading integration for customer %d at %s: %v", row.CustomerID, row.Provider, err)
		return row
	}
	if !hasExternalCustomer(link) {
		markApplication(row, false, s.factory)
		return row
	}
	refreshed, err := s.Refresh(ctx, row.CustomerID, row.Provider)
	if err != nil {
		facades.Log().Warningf("kyc: reset: refresh for customer %d at %s: %v", row.CustomerID, row.Provider, err)
		return row
	}
	markApplication(refreshed, true, s.factory)
	return refreshed
}

// clearVerdict returns a row to what a fresh one holds, except for the terms:
// tos_status, the agreement id and the terms session stay, since Bridge never
// echoes the agreement id back and losing it loops the user on the terms step.
// The jsonb lists are written empty — the columns are NOT NULL.
func clearVerdict(row *models.CustomerProviderKyc, preferredMode string) {
	row.KycStatus = models.KycStatusNotStarted
	row.PreferredMode, row.CollectionMode = preferredMode, preferredMode
	row.Endorsements = "[]"
	row.SetRequirements(nil)
	row.SetRejectionReasons(nil)
	row.SetRejectionCategories(nil)
	row.SetRejectionDetails(nil)
	row.RawStatus = "{}"
	row.HostedKycLinkID, row.HostedKycURL = nil, nil
	row.ClearSubmissionFailure()
	row.LastSyncedAt = nil
}
