package ramps

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// PayoutDestinationService is the admin's read of where a user's USD payouts
// go: the US bank accounts they saved and the liquidation addresses that pay
// them. Read only (spec D14).
type PayoutDestinationService struct {
	banks     repositories.UserBankAccountRepository
	addresses repositories.UserLiquidationAddressRepository
}

// NewPayoutDestinationService wires the service.
func NewPayoutDestinationService(
	banks repositories.UserBankAccountRepository,
	addresses repositories.UserLiquidationAddressRepository,
) *PayoutDestinationService {
	return &PayoutDestinationService{banks: banks, addresses: addresses}
}

// ListBankAccounts is every account the user saved, deleted ones included:
// an old payout still points at the account it paid.
func (s *PayoutDestinationService) ListBankAccounts(userID uint) ([]repositories.UserBankAccountView, error) {
	rows, err := s.banks.ListByUserWithDeleted(userID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: list bank accounts: %w", err)
	}
	return rows, nil
}

// ListCustomerBankAccounts is ListBankAccounts for a customer, a company's
// included: the accounts it pays, deleted ones too.
func (s *PayoutDestinationService) ListCustomerBankAccounts(customerID uint) ([]repositories.UserBankAccountView, error) {
	rows, err := s.banks.ListByCustomerWithDeleted(customerID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: list customer bank accounts: %w", err)
	}
	return rows, nil
}

// ListCustomerLiquidationAddresses is every payout address whose route is the
// customer's, oldest first.
func (s *PayoutDestinationService) ListCustomerLiquidationAddresses(customerID uint) ([]models.UserLiquidationAddress, error) {
	rows, err := s.addresses.ListByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: list customer liquidation addresses: %w", err)
	}
	return rows, nil
}

// ListLiquidationAddresses is every payout address of the user, oldest first.
func (s *PayoutDestinationService) ListLiquidationAddresses(userID uint) ([]models.UserLiquidationAddress, error) {
	rows, err := s.addresses.ListByUser(userID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: list liquidation addresses: %w", err)
	}
	return rows, nil
}
