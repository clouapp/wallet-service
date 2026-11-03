package repositories

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
)

// ErrDuplicateBankAccount is Create refusing an account the user already
// saved: the same routing and account digits (account_hash).
var ErrDuplicateBankAccount = errors.New("duplicate bank account")

// ErrBankAccountNotFound is UpdateLabel or Delete given an id that does not
// exist or belongs to another user — indistinguishable on purpose, so a
// caller always maps it to 404, never 403.
var ErrBankAccountNotFound = errors.New("bank account not found")

// UserBankAccountInput is what a caller supplies to save an account, already
// normalized: routing and account as digits, AccountHash from
// usbank.AccountHash.
type UserBankAccountInput struct {
	Label, OwnerName, BankName, RoutingNumber, AccountNumber, AccountType, AccountHash string
	Address                                                                            models.BankAccountAddress
}

// UserBankAccountView is the decrypted account. Repositories never hand out
// the encrypted row or the hash.
type UserBankAccountView struct {
	ID uint
	// UserID is who saved it.
	UserID                                                   uint
	Label, OwnerName, BankName, RoutingNumber, AccountNumber string
	RoutingLast4, AccountLast4, AccountType                  string
	Address                                                  models.BankAccountAddress
	CreatedAt                                                *time.Time
	DeletedAt                                                *time.Time
}

// UserBankAccountRepository is the persistence boundary for saved US bank
// accounts.
type UserBankAccountRepository interface {
	// ListByUser is the user's live accounts, oldest first.
	ListByUser(userID uint) ([]UserBankAccountView, error)
	// ListByUserWithDeleted adds the deleted ones: the admin's history.
	ListByUserWithDeleted(userID uint) ([]UserBankAccountView, error)
	// ListByCustomerWithDeleted is the admin's history of the accounts a
	// customer pays: those registered for it at a provider or paid by one of
	// its liquidation addresses and, for an individual customer, every account
	// its user saved (the customer's id is the user's). Deleted ones included.
	ListByCustomerWithDeleted(customerID uint) ([]UserBankAccountView, error)
	// FindOwned answers (nil, nil) for a miss or another user's id.
	FindOwned(userID, id uint) (*UserBankAccountView, error)
	// FindByIDWithTrashed is one account, a deleted one included, for the
	// order that paid into it; (nil, nil) for a miss. The caller checks whose
	// it is.
	FindByIDWithTrashed(id uint) (*UserBankAccountView, error)
	// Create answers ErrDuplicateBankAccount for an account already saved.
	Create(userID uint, input UserBankAccountInput) (*UserBankAccountView, error)
	// UpdateLabel answers ErrBankAccountNotFound for a miss.
	UpdateLabel(userID, id uint, label string) (*UserBankAccountView, error)
	// Delete soft-deletes; ErrBankAccountNotFound for a miss.
	Delete(userID, id uint) error
}

type userBankAccountRepository struct{}

// NewUserBankAccountRepository returns a UserBankAccountRepository backed by
// the default ORM connection.
func NewUserBankAccountRepository() UserBankAccountRepository {
	return &userBankAccountRepository{}
}

func (r *userBankAccountRepository) ListByUser(userID uint) ([]UserBankAccountView, error) {
	var rows []models.UserBankAccount
	if err := facades.Orm().Query().Where("user_id", userID).Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user bank account repo: list: %w", err)
	}
	return bankAccountViews(rows)
}

func (r *userBankAccountRepository) ListByUserWithDeleted(userID uint) ([]UserBankAccountView, error) {
	var rows []models.UserBankAccount
	if err := facades.Orm().Query().WithTrashed().Where("user_id", userID).Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user bank account repo: list with deleted: %w", err)
	}
	return bankAccountViews(rows)
}

func (r *userBankAccountRepository) ListByCustomerWithDeleted(customerID uint) ([]UserBankAccountView, error) {
	const paid = "id IN (SELECT user_bank_account_id FROM bank_account_destinations WHERE customer_id = ?" +
		" UNION SELECT user_bank_account_id FROM user_liquidation_addresses WHERE customer_id = ?)"
	query := facades.Orm().Query().WithTrashed()
	if models.IsIndividualCustomerID(customerID) {
		query = query.Where("(user_id = ? OR "+paid+")", customerID, customerID, customerID)
	} else {
		query = query.Where(paid, customerID, customerID)
	}
	var rows []models.UserBankAccount
	if err := query.Order("id asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user bank account repo: list by customer with deleted: %w", err)
	}
	return bankAccountViews(rows)
}

func (r *userBankAccountRepository) FindOwned(userID, id uint) (*UserBankAccountView, error) {
	row, err := r.findOwned(userID, id)
	if err != nil || row == nil {
		return nil, err
	}
	return bankAccountRowToView(row)
}

func (r *userBankAccountRepository) FindByIDWithTrashed(id uint) (*UserBankAccountView, error) {
	row, err := first[models.UserBankAccount](facades.Orm().Query().WithTrashed().Where("id", id),
		"user bank account repo: find by id with trashed")
	if err != nil || row == nil {
		return nil, err
	}
	return bankAccountRowToView(row)
}

func (r *userBankAccountRepository) Create(userID uint, input UserBankAccountInput) (*UserBankAccountView, error) {
	row, err := encryptedBankAccountRow(userID, input)
	if err != nil {
		return nil, err
	}
	if err := facades.Orm().Query().Create(row); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateBankAccount
		}
		return nil, fmt.Errorf("user bank account repo: create: %w", err)
	}
	return bankAccountRowToView(row)
}

func (r *userBankAccountRepository) UpdateLabel(userID, id uint, label string) (*UserBankAccountView, error) {
	row, err := r.findOwned(userID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrBankAccountNotFound
	}
	encrypted, err := encryptField(label)
	if err != nil {
		return nil, fmt.Errorf("user bank account repo: encrypt label: %w", err)
	}
	row.Label = encrypted
	if err := facades.Orm().Query().Save(row); err != nil {
		return nil, fmt.Errorf("user bank account repo: update label: %w", err)
	}
	return bankAccountRowToView(row)
}

func (r *userBankAccountRepository) Delete(userID, id uint) error {
	row, err := r.findOwned(userID, id)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrBankAccountNotFound
	}
	if _, err := facades.Orm().Query().Delete(row); err != nil {
		return fmt.Errorf("user bank account repo: delete: %w", err)
	}
	return nil
}

func (r *userBankAccountRepository) findOwned(userID, id uint) (*models.UserBankAccount, error) {
	return first[models.UserBankAccount](facades.Orm().Query().Where("user_id", userID).Where("id", id),
		"user bank account repo: find owned")
}

func encryptedBankAccountRow(userID uint, input UserBankAccountInput) (*models.UserBankAccount, error) {
	address, err := json.Marshal(input.Address)
	if err != nil {
		return nil, fmt.Errorf("user bank account repo: encode address: %w", err)
	}
	plain := []string{input.Label, input.OwnerName, input.BankName, input.RoutingNumber, input.AccountNumber, string(address)}
	sealed := make([]string, len(plain))
	for i, value := range plain {
		if sealed[i], err = encryptField(value); err != nil {
			return nil, fmt.Errorf("user bank account repo: encrypt field %d: %w", i, err)
		}
	}
	return &models.UserBankAccount{
		UserID: userID, Label: sealed[0], AccountOwnerName: sealed[1], BankName: sealed[2],
		RoutingNumber: sealed[3], AccountNumber: sealed[4], Address: sealed[5],
		RoutingLast4: digits.Last4(input.RoutingNumber), AccountLast4: digits.Last4(input.AccountNumber),
		AccountType: input.AccountType, AccountHash: input.AccountHash,
	}, nil
}

func bankAccountViews(rows []models.UserBankAccount) ([]UserBankAccountView, error) {
	views := make([]UserBankAccountView, 0, len(rows))
	for i := range rows {
		view, err := bankAccountRowToView(&rows[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func bankAccountRowToView(row *models.UserBankAccount) (*UserBankAccountView, error) {
	sealed := []string{row.Label, row.AccountOwnerName, row.BankName, row.RoutingNumber, row.AccountNumber, row.Address}
	plain := make([]string, len(sealed))
	for i, value := range sealed {
		var err error
		if plain[i], err = decryptField(value); err != nil {
			return nil, fmt.Errorf("user bank account repo: decrypt field %d of account %d: %w", i, row.ID, err)
		}
	}
	var address models.BankAccountAddress
	if err := json.Unmarshal([]byte(plain[5]), &address); err != nil {
		return nil, fmt.Errorf("user bank account repo: decode address of account %d: %w", row.ID, err)
	}
	view := &UserBankAccountView{
		ID: row.ID, UserID: row.UserID, Label: plain[0], OwnerName: plain[1], BankName: plain[2],
		RoutingNumber: plain[3], AccountNumber: plain[4],
		RoutingLast4: row.RoutingLast4, AccountLast4: row.AccountLast4, AccountType: row.AccountType,
		Address: address,
	}
	if row.CreatedAt != nil {
		created := row.CreatedAt.StdTime()
		view.CreatedAt = &created
	}
	if row.DeletedAt.Valid {
		deleted := row.DeletedAt.Time
		view.DeletedAt = &deleted
	}
	return view, nil
}

// DeleteByUserTx removes every bank account of the user through q, outright:
// the account deletion runs it in its own transaction (userRepository.
// DeleteAudited), and like the Pix keys nothing of the PII stays behind.
func (r *userBankAccountRepository) DeleteByUserTx(q orm.Query, userID uint) error {
	if _, err := q.Where("user_id", userID).ForceDelete(&models.UserBankAccount{}); err != nil {
		return fmt.Errorf("user bank account repo: delete by user: %w", err)
	}
	return nil
}
