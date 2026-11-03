package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrDuplicateLiquidationAddressRoute is Create losing uniq_ula_route: the
// route already holds a reservation or an active address, written by a request
// that raced this one.
var ErrDuplicateLiquidationAddressRoute = errors.New("duplicate liquidation address route")

// UserLiquidationAddressRepository is the persistence boundary for a user's
// standing payout addresses.
type UserLiquidationAddressRepository interface {
	// FindActiveRoute returns the active address of a route: the customer, the
	// bank account, rail, chain and token. The oldest wins if a bug ever
	// produced two, so repeated calls answer the same address.
	FindActiveRoute(customerID, bankAccountID uint, rail, chain, currency string) (*models.UserLiquidationAddress, error)
	// FindProvisioningRoute returns the route's in-flight reservation.
	FindProvisioningRoute(customerID, bankAccountID uint, rail, chain, currency string) (*models.UserLiquidationAddress, error)
	// FindByID resolves transactions.liquidation_address_id. It does not filter
	// by status: a drain still reconciles after its address went inactive.
	FindByID(id uint) (*models.UserLiquidationAddress, error)
	// FindByProviderAndExternalID is how a drain event finds its user. It does
	// not filter by status either.
	FindByProviderAndExternalID(provider, externalAddressID string) (*models.UserLiquidationAddress, error)
	// ListByUser lists every address of the user, oldest first.
	ListByUser(userID uint) ([]models.UserLiquidationAddress, error)
	// ListByCustomer lists every address whose route is the customer's, oldest
	// first: a company's, whoever of its members provisioned them.
	ListByCustomer(customerID uint) ([]models.UserLiquidationAddress, error)
	Create(row *models.UserLiquidationAddress) error
	Update(row *models.UserLiquidationAddress) error
	// DeactivateByBankAccount marks every address of the user's bank account
	// inactive, reservations included: the account was deleted. The rows stay,
	// transactions point at them.
	DeactivateByBankAccount(userID, bankAccountID uint) error
	// Delete hard-deletes a dead reservation, so the route can be provisioned
	// again.
	Delete(row *models.UserLiquidationAddress) error
}

type userLiquidationAddressRepository struct{}

func NewUserLiquidationAddressRepository() UserLiquidationAddressRepository {
	return &userLiquidationAddressRepository{}
}

func (r *userLiquidationAddressRepository) FindActiveRoute(customerID, bankAccountID uint, rail, chain, currency string) (*models.UserLiquidationAddress, error) {
	return r.findRoute(customerID, bankAccountID, rail, chain, currency, models.LiquidationAddressStatusActive)
}

func (r *userLiquidationAddressRepository) FindProvisioningRoute(customerID, bankAccountID uint, rail, chain, currency string) (*models.UserLiquidationAddress, error) {
	return r.findRoute(customerID, bankAccountID, rail, chain, currency, models.LiquidationAddressStatusProvisioning)
}

func (r *userLiquidationAddressRepository) findRoute(customerID, bankAccountID uint, rail, chain, currency, status string) (*models.UserLiquidationAddress, error) {
	return first[models.UserLiquidationAddress](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("user_bank_account_id", bankAccountID).
		Where("rail", rail).
		Where("chain", chain).
		Where("currency", currency).
		Where("status", status).
		Order("id asc"), "user liquidation address repo: find "+status+" route")
}

func (r *userLiquidationAddressRepository) FindByID(id uint) (*models.UserLiquidationAddress, error) {
	return first[models.UserLiquidationAddress](facades.Orm().Query().Where("id", id),
		"user liquidation address repo: find by id")
}

func (r *userLiquidationAddressRepository) FindByProviderAndExternalID(provider, externalAddressID string) (*models.UserLiquidationAddress, error) {
	return first[models.UserLiquidationAddress](facades.Orm().Query().
		Where("provider", provider).
		Where("external_address_id", externalAddressID), "user liquidation address repo: find by provider+external id")
}

func (r *userLiquidationAddressRepository) ListByUser(userID uint) ([]models.UserLiquidationAddress, error) {
	var rows []models.UserLiquidationAddress
	if err := facades.Orm().Query().Where("user_id", userID).Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user liquidation address repo: list by user: %w", err)
	}
	return rows, nil
}

func (r *userLiquidationAddressRepository) ListByCustomer(customerID uint) ([]models.UserLiquidationAddress, error) {
	var rows []models.UserLiquidationAddress
	if err := facades.Orm().Query().Where("customer_id", customerID).Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user liquidation address repo: list by customer: %w", err)
	}
	return rows, nil
}

// Create writes a reservation only while the member provisioning it is still
// a live account and the customer is not closed, read FOR SHARE as
// customerVirtualAccountRepository.Create reads them: a provision
// authenticated before the account's deletion committed is refused with
// ErrUserDeleted, one for a customer closed first with ErrCustomerClosed.
func (r *userLiquidationAddressRepository) Create(row *models.UserLiquidationAddress) error {
	err := facades.Orm().Transaction(func(q orm.Query) error {
		if err := lockLiveUser(q, row.UserID); err != nil {
			return err
		}
		if err := lockOpenCustomer(q, row.CustomerID); err != nil {
			return err
		}
		return q.Create(row)
	})
	if isUniqueViolation(err) {
		return ErrDuplicateLiquidationAddressRoute
	}
	if err != nil {
		return fmt.Errorf("user liquidation address repo: create: %w", err)
	}
	return nil
}

func (r *userLiquidationAddressRepository) Update(row *models.UserLiquidationAddress) error {
	if err := facades.Orm().Query().Save(row); err != nil {
		return fmt.Errorf("user liquidation address repo: update: %w", err)
	}
	return nil
}

func (r *userLiquidationAddressRepository) Delete(row *models.UserLiquidationAddress) error {
	if _, err := facades.Orm().Query().ForceDelete(row); err != nil {
		return fmt.Errorf("user liquidation address repo: delete: %w", err)
	}
	return nil
}

// DeactivateByUserTx marks every live address of the user inactive through q,
// for the account deletion. The rows stay: transactions point at them.
func (r *userLiquidationAddressRepository) DeactivateByUserTx(q orm.Query, userID uint) error {
	if _, err := q.Model(&models.UserLiquidationAddress{}).Where("user_id", userID).
		Update("status", models.LiquidationAddressStatusInactive); err != nil {
		return fmt.Errorf("user liquidation address repo: deactivate by user: %w", err)
	}
	return nil
}

func (r *userLiquidationAddressRepository) DeactivateByBankAccount(userID, bankAccountID uint) error {
	if _, err := facades.Orm().Query().Model(&models.UserLiquidationAddress{}).
		Where("user_id", userID).
		Where("user_bank_account_id", bankAccountID).
		Update("status", models.LiquidationAddressStatusInactive); err != nil {
		return fmt.Errorf("user liquidation address repo: deactivate by bank account: %w", err)
	}
	return nil
}
