package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrNotProvisioning answers the release of a reservation that is no longer
// one: finalized into a live account, or already released.
var ErrNotProvisioning = errors.New("virtual account not provisioning")

// CustomerVirtualAccountRepository is the persistence boundary for a customer's
// standing deposit account.
type CustomerVirtualAccountRepository interface {
	// FindActiveByCustomerAndCurrency asks the same question across every
	// provider: does this customer already have somewhere to deposit this
	// currency?
	//
	// It exists because a standing account is sticky in a way an order is not.
	// The provider-scoped lookup cannot see an account another provider already
	// gave the customer, so a change to the rail order would provision a second
	// one — handing them a new deposit code for one they already saved in their
	// bank app. The rail order decides where *new* accounts are created and never
	// touches an existing one.
	//
	// The oldest row wins if a bug ever produced two, so repeated calls answer
	// the same account rather than alternating between them.
	FindActiveByCustomerAndCurrency(customerID uint, sourceCurrency string) (*models.CustomerVirtualAccount, error)
	// FindProvisioningByCustomerAndCurrency returns an in-flight reservation while
	// the provider is being called outside the advisory lock. It is how a retry
	// or a concurrent provision joins the same attempt instead of opening a
	// second one.
	FindProvisioningByCustomerAndCurrency(customerID uint, sourceCurrency string) (*models.CustomerVirtualAccount, error)
	// FindByProviderAndExternalID resolves a provider-side account id back to the
	// local row, which is how an inbound deposit event finds its customer. It does
	// not filter by status: a deposit can land on an account after it was
	// deactivated, and the event still has to say whose it was. A reservation
	// still provisioning is the exception: its id is the placeholder XIP minted
	// (provisioning:<customer>:<currency>), never one the provider gave, so an
	// event naming it is no provider's and resolves nothing.
	FindByProviderAndExternalID(provider, externalAccountID string) (*models.CustomerVirtualAccount, error)
	// FindByID resolves our own foreign key back to the row, which is what the
	// reconciliation sweep holds: a transaction carries `virtual_account_id` and
	// no provider-side id, and reading the deposit back out of the history needs
	// the external account id this row keeps.
	//
	// It does not filter by status, for the same reason FindByProviderAndExternalID
	// does not: an order created while the account was active still has to
	// reconcile after somebody deactivated it.
	FindByID(id uint) (*models.CustomerVirtualAccount, error)
	// FindActiveByProvider lists up to limit active accounts for the sync
	// command to compare against the provider. A deactivated account has nothing
	// left to diverge. Ordered by id so a capped walk is a stable prefix rather
	// than an arbitrary sample.
	FindActiveByProvider(provider string, limit int) ([]models.CustomerVirtualAccount, error)
	// FindByCustomer lists every account a customer has ever held, deactivated
	// ones included. It serves the admin view, where the deactivated account is often
	// the interesting one: it is where the money an operator is chasing was sent,
	// and by the time anybody asks it is already history.
	FindByCustomer(customerID uint) ([]models.CustomerVirtualAccount, error)
	// HasOpenByCustomerAndProvider reports whether the customer holds an account at
	// provider that is active or still being provisioned — one that can
	// receive money there.
	HasOpenByCustomerAndProvider(customerID uint, provider string) (bool, error)
	Create(account *models.CustomerVirtualAccount) error
	Update(account *models.CustomerVirtualAccount) error
	// Delete soft-deletes the account. Both uniques are partial over deleted_at,
	// so re-provisioning afterwards is allowed.
	Delete(account *models.CustomerVirtualAccount) error
	// ReleaseReservation soft-deletes a reservation still provisioning and
	// writes entry — the audit row of the operator act that asked for it — in
	// the same transaction. A row that is no longer provisioning and live (the
	// client finalized it into a deposit code, or a second click already
	// released it) is left alone: ErrNotProvisioning, nothing recorded.
	ReleaseReservation(account *models.CustomerVirtualAccount, entry *models.AdminAuditLog) error
	// DestinationOfAnother reports whether a customer other than customerID
	// holds a standing account still delivering to address on chain — active,
	// or being provisioned: the one place an address is a holder's without
	// being registered as theirs (spec §3.1). A deactivated or deleted account
	// delivers to nothing.
	DestinationOfAnother(customerID uint, chain, address string) (bool, error)
}

type customerVirtualAccountRepository struct{ tenant, environment string }

// NewCustomerVirtualAccountRepository returns a CustomerVirtualAccountRepository backed
// by the default ORM connection.
func NewCustomerVirtualAccountRepository() CustomerVirtualAccountRepository {
	return &customerVirtualAccountRepository{}
}

func (r *customerVirtualAccountRepository) FindProvisioningByCustomerAndCurrency(customerID uint, sourceCurrency string) (*models.CustomerVirtualAccount, error) {
	return first[models.CustomerVirtualAccount](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("source_currency", sourceCurrency).
		Where("status", models.VirtualAccountStatusProvisioning).
		Order("id asc"), "customer virtual account repo: find provisioning by customer+currency")
}

func (r *customerVirtualAccountRepository) FindActiveByCustomerAndCurrency(customerID uint, sourceCurrency string) (*models.CustomerVirtualAccount, error) {
	return first[models.CustomerVirtualAccount](facades.Orm().Query().
		Where("customer_id", customerID).
		Where("source_currency", sourceCurrency).
		Where("status", models.VirtualAccountStatusActive).
		Order("id asc"), "customer virtual account repo: find active by customer+currency")
}

func (r *customerVirtualAccountRepository) FindByProviderAndExternalID(provider, externalAccountID string) (*models.CustomerVirtualAccount, error) {
	return first[models.CustomerVirtualAccount](facades.Orm().Query().
		Where("provider", provider).
		Where("external_account_id", externalAccountID).
		Where("status <> ?", models.VirtualAccountStatusProvisioning),
		"customer virtual account repo: find by provider+external id")
}

func (r *customerVirtualAccountRepository) FindByID(id uint) (*models.CustomerVirtualAccount, error) {
	return first[models.CustomerVirtualAccount](facades.Orm().Query().Where("id", id), "customer virtual account repo: find by id")
}

func (r *customerVirtualAccountRepository) FindActiveByProvider(provider string, limit int) ([]models.CustomerVirtualAccount, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("customer virtual account repo: find active by provider: limit must be positive")
	}
	var accounts []models.CustomerVirtualAccount
	if err := facades.Orm().Query().
		Where("provider", provider).
		Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).
		Where("status", models.VirtualAccountStatusActive).
		Order("id asc").
		Limit(limit).
		Find(&accounts); err != nil {
		return nil, fmt.Errorf("customer virtual account repo: find active by provider: %w", err)
	}
	return accounts, nil
}

func (r *customerVirtualAccountRepository) FindByCustomer(customerID uint) ([]models.CustomerVirtualAccount, error) {
	var accounts []models.CustomerVirtualAccount
	if err := facades.Orm().Query().
		Where("customer_id", customerID).
		Order("id asc").
		Find(&accounts); err != nil {
		return nil, fmt.Errorf("customer virtual account repo: find by customer: %w", err)
	}
	return accounts, nil
}

// Create writes a standing-account reservation, and only while the member
// provisioning it is still a live account (lockLiveUser): one provisioned by a
// request that was authenticated before the account's deletion committed is
// refused with ErrUserDeleted. Likewise for the customer (lockOpenCustomer):
// one whose close committed first refuses it with ErrCustomerClosed.
func (r *customerVirtualAccountRepository) Create(account *models.CustomerVirtualAccount) error {
	return facades.Orm().Transaction(func(q orm.Query) error {
		if err := lockLiveUser(q, account.UserID); err != nil {
			return fmt.Errorf("customer virtual account repo: create: %w", err)
		}
		if err := lockOpenCustomer(q, account.CustomerID); err != nil {
			return fmt.Errorf("customer virtual account repo: create: %w", err)
		}
		if err := q.Create(account); err != nil {
			return fmt.Errorf("customer virtual account repo: create: %w", err)
		}
		return nil
	})
}

func (r *customerVirtualAccountRepository) Update(account *models.CustomerVirtualAccount) error {
	return r.UpdateTx(facades.Orm().Query(), account)
}

func (r *customerVirtualAccountRepository) UpdateTx(q orm.Query, account *models.CustomerVirtualAccount) error {
	if err := q.Save(account); err != nil {
		return fmt.Errorf("customer virtual account repo: update: %w", err)
	}
	return nil
}

func (r *customerVirtualAccountRepository) Delete(account *models.CustomerVirtualAccount) error {
	if _, err := facades.Orm().Query().Delete(account); err != nil {
		return fmt.Errorf("customer virtual account repo: delete: %w", err)
	}
	return nil
}

func (r *customerVirtualAccountRepository) ReleaseReservation(account *models.CustomerVirtualAccount, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		res, err := tx.Where("status", models.VirtualAccountStatusProvisioning).Delete(account)
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return ErrNotProvisioning
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("customer virtual account repo: release reservation: %w", err)
	}
	return nil
}

func (r *customerVirtualAccountRepository) HasOpenByCustomerAndProvider(customerID uint, provider string) (bool, error) {
	return r.HasOpenByCustomerAndProviderTx(facades.Orm().Query(), customerID, provider)
}

// HasOpenByCustomerTx reports, through q, whether the customer holds an
// account at any provider that is active or still being provisioned: the
// deletion's guard, read inside its own transaction.
func (r *customerVirtualAccountRepository) HasOpenByCustomerTx(q orm.Query, customerID uint) (bool, error) {
	open, err := q.Model(&models.CustomerVirtualAccount{}).
		Where("customer_id", customerID).
		Where("status IN ?", []string{models.VirtualAccountStatusActive, models.VirtualAccountStatusProvisioning}).
		Exists()
	if err != nil {
		return false, fmt.Errorf("customer virtual account repo: has open by customer: %w", err)
	}
	return open, nil
}

// HasOpenByCustomerAndProviderTx is HasOpenByCustomerAndProvider through q, for the
// unlink that checks again inside its own transaction.
func (r *customerVirtualAccountRepository) HasOpenByCustomerAndProviderTx(q orm.Query, customerID uint, provider string) (bool, error) {
	open, err := q.Model(&models.CustomerVirtualAccount{}).
		Where("customer_id", customerID).
		Where("provider", provider).
		Where("status IN ?", []string{models.VirtualAccountStatusActive, models.VirtualAccountStatusProvisioning}).
		Exists()
	if err != nil {
		return false, fmt.Errorf("customer virtual account repo: has open by customer and provider: %w", err)
	}
	return open, nil
}

func (r *customerVirtualAccountRepository) DestinationOfAnother(customerID uint, chain, address string) (bool, error) {
	held, err := facades.Orm().Query().Model(&models.CustomerVirtualAccount{}).
		Where("customer_id <> ?", customerID).
		Where("destination_chain", chain).
		Where("destination_address", address).
		Where("status IN ?", []string{models.VirtualAccountStatusActive, models.VirtualAccountStatusProvisioning}).
		Exists()
	if err != nil {
		return false, fmt.Errorf("customer virtual account repo: destination of another: %w", err)
	}
	return held, nil
}

// NewCustomerVirtualAccountRepositoryForTenant bounds background listings before claiming work.
func NewCustomerVirtualAccountRepositoryForTenant(tenant, environment string) CustomerVirtualAccountRepository {
	return &customerVirtualAccountRepository{tenant: tenant, environment: environment}
}
