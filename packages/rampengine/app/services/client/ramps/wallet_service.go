package ramps

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrWalletInUse refuses registering an address another holder holds: one
// another customer registered, or the destination of another customer's
// standing deposit account (spec §3.1).
var ErrWalletInUse = errors.New("ramps: wallet in use")

// ErrWalletNotForPerson refuses a person's wallet registration: no money
// rule of a person reads the registry, the app registers only a company's,
// and an address a person took would be a company's lost for good, the
// registration being unique per address (round-2 review). 403 forbidden.
var ErrWalletNotForPerson = errors.New("ramps: a person registers no wallet")

// CustomerApproval answers whether a customer is approved at a provider yet:
// nil, or the *ramp.KycBlocked refusal the app opens the KYC with.
// kycsvc.ProviderKycService implements it.
type CustomerApproval interface {
	RequireApproved(customerID uint) error
}

// WalletDeps is what a WalletService is built from, filled by name.
type WalletDeps struct {
	Wallets   repositories.CustomerWalletRepository
	Accounts  repositories.CustomerVirtualAccountRepository
	Approval  CustomerApproval
	MfaStepUp MfaStepUpValidator
	// Activation reconciles an approved business draft before refusing it
	// as not active. Optional: without it a draft is refused.
	Activation BusinessActivation
}

// WalletService registers the addresses a customer holds as its own (spec §9,
// API contract §9): a business's deposits arrive only to, and its
// withdrawals leave only from, one of them.
type WalletService struct {
	wallets    repositories.CustomerWalletRepository
	accounts   repositories.CustomerVirtualAccountRepository
	approval   CustomerApproval
	mfaStepUp  MfaStepUpValidator
	activation BusinessActivation
}

// NewWalletService wires the service.
func NewWalletService(deps WalletDeps) *WalletService {
	return &WalletService{wallets: deps.Wallets, accounts: deps.Accounts, approval: deps.Approval, mfaStepUp: deps.MfaStepUp,
		activation: deps.Activation}
}

// Register registers in's address to customer for user, in the contract's
// order: a person is refused (ErrWalletNotForPerson); a business not yet
// approved at any provider hears the KYC refusal (kyc_pending, kyc_rejected)
// before any MFA attempt is spent, and so does one approved but not active
// (kyc_pending); then the user's step-up, as changing the deposit account
// asks for; then an address another holder holds — another customer's
// registration, or another customer's standing account destination — is
// ErrWalletInUse; then the customer's own address again answers its
// registration unchanged (created false).
//
// Registering does not prove possession of the address (spec §16): the
// step-up and the uniqueness are the defences.
func (s *WalletService) Register(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.RegisterWalletDTO,
) (*models.CustomerWallet, bool, error) {
	if customer.Type != models.CustomerTypeBusiness {
		return nil, false, ErrWalletNotForPerson
	}
	if err := s.approval.RequireApproved(customer.ID); err != nil {
		return nil, false, fmt.Errorf("ramps: register wallet: %w", err)
	}
	// Approved is not enough: a draft another company's activation outran
	// holds a document that company holds, and money of a business that is
	// not active is refused (spec §9). An approved draft is reconciled first,
	// as the money rules do.
	if err := activeBusiness(customer, s.activation); err != nil {
		return nil, false, fmt.Errorf("ramps: register wallet: %w", err)
	}
	if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
		return nil, false, err
	}
	wallet := &models.CustomerWallet{
		CustomerID: customer.ID, Chain: in.Chain, Address: strings.TrimSpace(in.Address), RegisteredByUserID: user.ID,
	}
	if held, err := s.heldBy(wallet); err != nil || held != nil {
		return held, false, err
	}
	if err := s.wallets.Create(wallet); err != nil {
		return s.lostRace(wallet, err)
	}
	return wallet, true, nil
}

// lostRace answers a registration the unique refused: another registration
// of the address won, and who it was is the answer a retry would get.
func (s *WalletService) lostRace(wallet *models.CustomerWallet, createErr error) (*models.CustomerWallet, bool, error) {
	if !errors.Is(createErr, repositories.ErrWalletTaken) {
		return nil, false, fmt.Errorf("ramps: register wallet: %w", createErr)
	}
	held, err := s.heldBy(wallet)
	if err == nil && held == nil {
		err = fmt.Errorf("ramps: register wallet: the address is taken by no registration: %w", createErr)
	}
	return held, false, err
}

// heldBy is the customer's own registration of wallet's address, ErrWalletInUse
// when another holder holds it, or (nil, nil) when nobody does.
func (s *WalletService) heldBy(wallet *models.CustomerWallet) (*models.CustomerWallet, error) {
	registered, err := s.wallets.FindByAddress(wallet.Chain, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("ramps: register wallet: %w", err)
	}
	if registered != nil {
		if registered.CustomerID != wallet.CustomerID {
			return nil, ErrWalletInUse
		}
		return registered, nil
	}
	destination, err := s.accounts.DestinationOfAnother(wallet.CustomerID, wallet.Chain, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("ramps: register wallet: %w", err)
	}
	if destination {
		return nil, ErrWalletInUse
	}
	return nil, nil
}
