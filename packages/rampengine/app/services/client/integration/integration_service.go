// Package integration links a local user to a customer on a payment provider.
//
// It replaces the role `receivers` played for Astronpay, with one difference
// that matters: the link is per provider, so a user can be live on one and
// untouched on another. Which rails the link actually opens is the KYC row's
// business — this package only answers "is there a customer over there, and may
// we use it".
package integration

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/jsonb"
)

// Service drives the integration lifecycle for the authenticated user. It
// returns models rather than DTOs: every column that must not reach a client
// is already marked `json:"-"` on the model, and the HTTP layer projects them
// through resources.
type Service struct {
	factory   *ramp.Factory
	integRepo repositories.CustomerProviderIntegrationRepository
	locker    locking.Locker
}

// NewService wires the integration service.
func NewService(
	factory *ramp.Factory,
	integRepo repositories.CustomerProviderIntegrationRepository,
	locker locking.Locker,
) *Service {
	return &Service{factory: factory, integRepo: integRepo, locker: locker}
}

// Start links the user to the provider, returning the existing link unchanged
// when there already is one.
//
// Concurrency: two simultaneous requests would otherwise both pass the dedup
// check and both insert, and the second insert would hit the partial unique on
// (user_id, provider) as a 500. The check-then-create is serialized behind an
// advisory lock, exactly as the Astronpay receiver flow is. The pre-lock read
// stays as a fast path — most calls are repeats and never take the lock.
//
// The adapter call happens before the lock is taken. WithLock holds a Postgres
// transaction open for as long as its callback runs, and an upstream request
// inside it would keep a connection `idle in transaction` for the provider's
// whole latency. Two concurrent starts may therefore both call the adapter, but
// StartIntegration confirms an existing customer rather than creating one, and
// the loser of the insert race discards its own result for the stored row.
func (s *Service) Start(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderIntegration, error) {
	link, _, err := s.StartReporting(ctx, customerID, provider)
	return link, err
}

// StartReporting is Start answering whether it created the link, for the
// admin, which records a start only when one was opened.
func (s *Service) StartReporting(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderIntegration, bool, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, false, err
	}

	existing, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, false, fmt.Errorf("integration: lookup: %w", err)
	}
	if existing != nil {
		return existing, false, nil
	}

	result, err := adapter.StartIntegration(ctx, ramp.CustomerRef{CustomerID: customerID})
	if err != nil {
		return nil, false, fmt.Errorf("integration: start at %s: %w", provider, err)
	}

	var link *models.CustomerProviderIntegration
	opened := false
	err = s.locker.WithLock(ctx, startLockKey(customerID, provider), func(context.Context) error {
		// Re-check inside the lock: a concurrent request may have committed the
		// row between our read and our acquisition.
		existing, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
		if err != nil {
			return fmt.Errorf("integration: lookup (locked): %w", err)
		}
		if existing != nil {
			link = existing
			return nil
		}

		row := &models.CustomerProviderIntegration{
			CustomerID: customerID,
			Provider:   provider,
			Status:     cmp.Or(result.Status, models.IntegrationStatusPending),
			Metadata:   jsonb.Object(result.Metadata),
		}
		if result.ExternalCustomerID != "" {
			row.ExternalCustomerID = &result.ExternalCustomerID
		}
		if err := s.integRepo.Create(row); err != nil {
			return fmt.Errorf("integration: persist: %w", err)
		}
		link, opened = row, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return link, opened, nil
}

// Refresh reconciles the local link with the provider.
func (s *Service) Refresh(ctx context.Context, customerID uint, provider string) (*models.CustomerProviderIntegration, error) {
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return nil, err
	}
	row, err := s.require(customerID, provider)
	if err != nil {
		return nil, err
	}
	// Nothing has been created upstream yet — the customer is born at the first
	// KYC submission — so there is nothing to reconcile against.
	if row.ExternalCustomerID == nil || *row.ExternalCustomerID == "" {
		return row, nil
	}

	result, err := adapter.RefreshIntegration(ctx, ramp.NewCustomerRef(row.CustomerID, row.ExternalCustomerID))
	if err != nil {
		return nil, fmt.Errorf("integration: refresh at %s: %w", provider, err)
	}

	row.Status = cmp.Or(result.Status, row.Status)
	if result.ExternalCustomerID != "" {
		row.ExternalCustomerID = &result.ExternalCustomerID
	}
	if len(result.Metadata) > 0 {
		row.Metadata = jsonb.Object(result.Metadata)
	}
	if err := s.integRepo.Update(row); err != nil {
		// Unlinked by an operator while the provider was asked: the link is
		// gone, as it would have been had the refresh started a moment later.
		if errors.Is(err, repositories.ErrIntegrationGone) {
			return nil, ramp.ErrIntegrationRequired
		}
		return nil, fmt.Errorf("integration: persist refresh: %w", err)
	}
	return row, nil
}

// List returns every provider link the user has.
func (s *Service) List(customerID uint) ([]models.CustomerProviderIntegration, error) {
	rows, err := s.integRepo.ListByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("integration: list: %w", err)
	}
	return rows, nil
}

// require loads the link or reports that the client has to start one first.
func (s *Service) require(customerID uint, provider string) (*models.CustomerProviderIntegration, error) {
	row, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, fmt.Errorf("integration: lookup: %w", err)
	}
	if row == nil {
		return nil, ramp.ErrIntegrationRequired
	}
	return row, nil
}

// Adopt links the user to a customer the provider already holds, for the
// admin relink of an orphan: a customer created upstream whose link row was
// never written. It asks the provider nothing — the caller has already read
// the customer and matched it to the account — and takes the lock Start
// takes, so a concurrent Start cannot open a second link. A link that exists
// is answered untouched with created false: rewriting it is the caller's
// relink, not an adoption.
func (s *Service) Adopt(ctx context.Context, customerID uint, provider, externalCustomerID string) (*models.CustomerProviderIntegration, bool, error) {
	if _, err := s.factory.Get(provider); err != nil {
		return nil, false, err
	}
	var link *models.CustomerProviderIntegration
	created := false
	err := s.locker.WithLock(ctx, startLockKey(customerID, provider), func(context.Context) error {
		existing, err := s.integRepo.FindByCustomerAndProvider(customerID, provider)
		if err != nil {
			return fmt.Errorf("integration: lookup (locked): %w", err)
		}
		if existing != nil {
			link = existing
			return nil
		}
		row := &models.CustomerProviderIntegration{
			CustomerID: customerID, Provider: provider, ExternalCustomerID: &externalCustomerID,
			Status: models.IntegrationStatusActive, Metadata: "{}",
		}
		if err := s.integRepo.Create(row); err != nil {
			return fmt.Errorf("integration: persist adoption: %w", err)
		}
		link, created = row, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return link, created, nil
}

// startLockKey serializes everything that may create a user's link at a
// provider.
func startLockKey(customerID uint, provider string) string {
	return fmt.Sprintf("integration:start:%d:%s", customerID, provider)
}
