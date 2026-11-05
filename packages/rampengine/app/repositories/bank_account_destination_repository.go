package repositories

import (
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrDuplicateBankAccountDestination is Create refusing a second registration
// of the same account at the same provider: another request recorded it first.
var ErrDuplicateBankAccountDestination = errors.New("duplicate bank account destination")

// BankAccountDestinationRepository is the persistence boundary for a bank
// account's registration at a provider.
type BankAccountDestinationRepository interface {
	// Find returns the customer's registration of the bank account at
	// provider, or nil when the provider never registered it for them.
	Find(customerID, bankAccountID uint, provider string) (*models.BankAccountDestination, error)
	// ListByBankAccount is every registration of the bank account, one per
	// customer and provider.
	ListByBankAccount(bankAccountID uint) ([]models.BankAccountDestination, error)
	// ListByUser is every registration of the user's live bank accounts, for
	// whichever customer: what an account deletion tells the providers to stop
	// paying. It is read before the deletion, which removes the accounts.
	ListByUser(userID uint) ([]models.BankAccountDestination, error)
	// Create records a registration. A second one for the same customer,
	// account and provider answers ErrDuplicateBankAccountDestination.
	Create(row *models.BankAccountDestination) error
}

type bankAccountDestinationRepository struct{}

func NewBankAccountDestinationRepository() BankAccountDestinationRepository {
	return &bankAccountDestinationRepository{}
}

func (r *bankAccountDestinationRepository) Find(customerID, bankAccountID uint, provider string) (*models.BankAccountDestination, error) {
	return first[models.BankAccountDestination](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("user_bank_account_id", bankAccountID).
		Where("provider", provider), "bank account destination repo: find")
}

func (r *bankAccountDestinationRepository) Create(row *models.BankAccountDestination) error {
	if row.PaymentRails == "" {
		row.PaymentRails = "[]"
	}
	if err := facades.Orm().Query().Create(row); err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateBankAccountDestination
		}
		return fmt.Errorf("bank account destination repo: create: %w", err)
	}
	return nil
}

func (r *bankAccountDestinationRepository) ListByBankAccount(bankAccountID uint) ([]models.BankAccountDestination, error) {
	var rows []models.BankAccountDestination
	if err := facades.Orm().Query().Where("user_bank_account_id", bankAccountID).Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("bank account destination repo: list by bank account: %w", err)
	}
	return rows, nil
}

func (r *bankAccountDestinationRepository) ListByUser(userID uint) ([]models.BankAccountDestination, error) {
	var rows []models.BankAccountDestination
	if err := facades.Orm().Query().
		Where("user_bank_account_id IN (SELECT id FROM user_bank_accounts WHERE user_id = ? AND deleted_at IS NULL)", userID).
		Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("bank account destination repo: list by user: %w", err)
	}
	return rows, nil
}
