package ramps

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
	emodels "github.com/macrowallets/waas/packages/rampengine/app/models"
)

var ErrCustomerNotFound = models.ErrRampCustomerNotFound
var ErrCustomerConflict = models.ErrRampCustomerConflict
var ErrOperationsPaused = errors.New("ramp operations paused")

type CustomerScope struct {
	Public  *models.RampCustomer
	Contact *emodels.User
	Holder  *emodels.Customer
	Member  *emodels.CustomerMember
}

func (s *Service) Customer(ctx context.Context, account *models.Account, id uuid.UUID) (*CustomerScope, error) {
	row, err := s.d.Customers.Find(ctx, account.ID, id)
	if err != nil {
		return nil, err
	}
	contact, holder, member, err := s.d.Customers.ContactAndHolder(ctx, row)
	if err != nil {
		return nil, err
	}
	mode, err := ModeForAccount(account)
	if err != nil {
		return nil, err
	}
	if holder.Environment != mode {
		return nil, ErrCustomerNotFound
	}
	return &CustomerScope{row, contact, holder, member}, nil
}
func (s *Service) Customers(ctx context.Context, account *models.Account, page, limit int, search string) ([]models.RampCustomer, int64, error) {
	return s.d.Customers.List(ctx, account.ID, page, limit, search)
}
func (s *Service) AccountHolder(ctx context.Context, account *models.Account) (*models.RampCustomer, error) {
	return s.d.Customers.AccountHolder(ctx, account.ID)
}
func (s *Service) CreateCustomer(ctx context.Context, account *models.Account, draft models.RampCustomerDraft) (*models.RampCustomer, bool, error) {
	if err := s.CheckNewOperations(ctx, account.ID, draft.Relationship); err != nil {
		return nil, false, err
	}
	if draft.Relationship == models.RampAccountHolder && draft.EntityType != "business" {
		return nil, false, fmt.Errorf("account holder must be a business")
	}
	if draft.EntityType == "business" {
		if err := s.d.Features.Gate(ctx, account.ID, "kyb", "kyb_disabled"); err != nil {
			return nil, false, ErrOperationsPaused
		}
	}
	return s.d.Customers.Create(ctx, account, draft)
}
func (s *Service) CheckNewOperations(ctx context.Context, accountID uuid.UUID, relationship string) error {
	flag := features.FlagRampCustomerOperations
	if relationship == models.RampAccountHolder {
		flag = features.FlagRampOwnOperations
	}
	if err := s.d.Features.Gate(ctx, accountID, flag, "ramp_operations_paused"); err != nil {
		return fmt.Errorf("%w: %v", ErrOperationsPaused, err)
	}
	return nil
}

// IntegrationsEnabled is the master switch: global rollout AND account opt-in.
// Existing callback/recovery processing deliberately does not use this gate.
func (s *Service) IntegrationsEnabled(ctx context.Context, accountID uuid.UUID) (bool, error) {
	if accountID == uuid.Nil {
		return false, nil
	}
	enabled, err := s.d.Features.GlobalEnabled(ctx, features.FlagRampIntegrations)
	if err != nil || !enabled {
		return false, err
	}
	if err := s.d.Features.Gate(ctx, accountID, features.FlagRampIntegrations, "ramp_integrations_disabled"); err != nil {
		var gate *features.GateError
		if errors.As(err, &gate) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
