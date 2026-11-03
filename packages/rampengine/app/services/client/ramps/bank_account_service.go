package ramps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
	"github.com/macrowallets/waas/packages/rampengine/app/support/usbank"
)

// ErrBankAccountNotFound is an account that does not exist or is another
// user's; the two are indistinguishable on purpose (404, never 403).
var ErrBankAccountNotFound = errors.New("ramps: bank account not found")

// ErrDuplicateBankAccount is an account the user already saved: the same
// routing and account digits.
var ErrDuplicateBankAccount = errors.New("ramps: bank account already registered")

// BankAccountView is a saved US bank account as the client sees it: the
// numbers only by their last four digits.
type BankAccountView struct {
	ID           uint
	Label        string
	OwnerName    string
	BankName     string
	AccountType  string
	AccountLast4 string
	RoutingLast4 string
	Address      models.BankAccountAddress
	CreatedAt    *time.Time
}

// BankAccountService owns the US bank accounts a user saves to receive USD
// payouts. It lives beside the payout services because it is one: the
// account is a payout destination, and creating one is guarded by the same
// MFA step-up a Pix payout is.
type BankAccountService struct {
	accounts  repositories.UserBankAccountRepository
	payouts   BankAccountPayouts
	mfaStepUp MfaStepUpValidator
	// locker serializes a delete with a payout address's provisioning, which
	// takes the same key (PayoutAddressLockKey).
	locker locking.Locker
}

// BankAccountPayouts stops the payout addresses of a deleted account.
// LiquidationAddressService is the one.
type BankAccountPayouts interface {
	// DeactivateBankAccountAddresses marks the account's addresses inactive,
	// so none is offered or reused again.
	DeactivateBankAccountAddresses(userID, bankAccountID uint) error
	// DeactivateBankAccountUpstream tells the provider to stop paying the
	// account, for every customer it was registered on. It is best effort and
	// logs a failure.
	DeactivateBankAccountUpstream(ctx context.Context, bankAccountID uint)
}

// DeletedAccountPayouts stops paying the bank accounts of a deleted user
// account. LiquidationAddressService is the one. The deletion force-deletes
// the accounts, so their registrations are read before it and deactivated at
// the provider after it commits.
type DeletedAccountPayouts interface {
	UserBankAccountRegistrations(userID uint) ([]models.BankAccountDestination, error)
	DeactivateRegistrationsUpstream(ctx context.Context, registrations []models.BankAccountDestination)
}

// NewBankAccountService wires the service.
func NewBankAccountService(
	accounts repositories.UserBankAccountRepository,
	payouts BankAccountPayouts,
	mfaStepUp MfaStepUpValidator,
	locker locking.Locker,
) *BankAccountService {
	return &BankAccountService{accounts: accounts, payouts: payouts, mfaStepUp: mfaStepUp, locker: locker}
}

// List is the user's saved accounts, oldest first.
func (s *BankAccountService) List(userID uint) ([]BankAccountView, error) {
	rows, err := s.accounts.ListByUser(userID)
	if err != nil {
		return nil, err
	}
	views := make([]BankAccountView, 0, len(rows))
	for i := range rows {
		views = append(views, toBankAccountView(&rows[i]))
	}
	return views, nil
}

// Create saves an account after the MFA step-up. Everything is normalized
// here, once: the numbers to their digits, the owner name's spaces collapsed,
// the state and country in capitals, the type in lowercase.
func (s *BankAccountService) Create(ctx context.Context, user identity.Subject, in dto.CreateBankAccountDTO) (*BankAccountView, error) {
	if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
		return nil, err
	}
	routing := digits.Only(in.RoutingNumber)
	account := digits.Only(in.AccountNumber)
	created, err := s.accounts.Create(user.SubjectID(), repositories.UserBankAccountInput{
		Label:         strings.TrimSpace(in.Label),
		OwnerName:     usbank.NormalizeOwnerName(in.OwnerName),
		BankName:      strings.TrimSpace(in.BankName),
		RoutingNumber: routing,
		AccountNumber: account,
		AccountType:   strings.ToLower(strings.TrimSpace(in.AccountType)),
		AccountHash:   usbank.AccountHash(routing, account),
		Address:       normalizeBankAddress(in.Address),
	})
	if errors.Is(err, repositories.ErrDuplicateBankAccount) {
		return nil, ErrDuplicateBankAccount
	}
	if err != nil {
		return nil, fmt.Errorf("ramps: save bank account: %w", err)
	}
	view := toBankAccountView(created)
	return &view, nil
}

// UpdateLabel renames an account. Nothing else of it is editable: other
// numbers are another account, deleted and saved anew.
func (s *BankAccountService) UpdateLabel(userID, id uint, label string) (*BankAccountView, error) {
	updated, err := s.accounts.UpdateLabel(userID, id, strings.TrimSpace(label))
	if errors.Is(err, repositories.ErrBankAccountNotFound) {
		return nil, ErrBankAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ramps: rename bank account: %w", err)
	}
	view := toBankAccountView(updated)
	return &view, nil
}

// Delete removes an account from the user's list. It is a soft delete: the
// orders paid into it keep pointing at it. Its payout addresses go inactive
// first, so a failure leaves the account with nothing active rather than an
// active address paying an account the user no longer sees; once the delete
// holds, the provider is told to stop paying it.
//
// Both writes run under the user's payout address lock (the user's individual
// customer is the only one a payout address is provisioned for, and its id is
// the user's): a provision either reserves before them, and is marked
// inactive with the rest, or reads the account gone under the same lock and
// stops (LiquidationAddressService.finalizeUnderLock).
func (s *BankAccountService) Delete(ctx context.Context, userID, id uint) error {
	err := s.locker.WithLock(ctx, PayoutAddressLockKey(userID), func(context.Context) error {
		if err := s.payouts.DeactivateBankAccountAddresses(userID, id); err != nil {
			return fmt.Errorf("ramps: deactivate payout addresses of bank account %d: %w", id, err)
		}
		err := s.accounts.Delete(userID, id)
		if errors.Is(err, repositories.ErrBankAccountNotFound) {
			return ErrBankAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("ramps: delete bank account: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.payouts.DeactivateBankAccountUpstream(ctx, id)
	return nil
}

func normalizeBankAddress(address models.BankAccountAddress) models.BankAccountAddress {
	return models.BankAccountAddress{
		StreetLine1: strings.TrimSpace(address.StreetLine1),
		StreetLine2: strings.TrimSpace(address.StreetLine2),
		City:        strings.TrimSpace(address.City),
		State:       strings.ToUpper(strings.TrimSpace(address.State)),
		PostalCode:  strings.TrimSpace(address.PostalCode),
		Country:     strings.ToUpper(strings.TrimSpace(address.Country)),
	}
}

func toBankAccountView(row *repositories.UserBankAccountView) BankAccountView {
	return BankAccountView{
		ID: row.ID, Label: row.Label, OwnerName: row.OwnerName, BankName: row.BankName,
		AccountType: row.AccountType, AccountLast4: row.AccountLast4, RoutingLast4: row.RoutingLast4,
		Address: row.Address, CreatedAt: row.CreatedAt,
	}
}
