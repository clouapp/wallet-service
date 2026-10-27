// Package walletactivation repairs an explicitly selected pending test wallet through its domain activation service.
package walletactivation

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	ErrSelection       = errors.New("exact account UUID, wallet UUID and expected chain are required")
	ErrBinding         = errors.New("activation refused: the exact active test account, pending owned wallet and active test chain must match")
	ErrCodeUnavailable = errors.New("activation refused: the selected pending wallet has no valid server-held activation code")
	ErrActivation      = errors.New("activation failed; inspect the selected wallet before retrying")
)

type Wallets interface {
	Within(context.Context, func(context.Context) error) error
	LockByID(context.Context, uuid.UUID) (*models.Wallet, error)
}
type Accounts interface {
	LockByID(context.Context, uuid.UUID) (*models.Account, error)
}
type Chains interface {
	LockByID(context.Context, string) (*models.Chain, error)
}
type Activator interface {
	ActivateWallet(context.Context, uuid.UUID, string) (*models.Wallet, error)
}

type Service struct {
	wallets  Wallets
	accounts Accounts
	chains   Chains
	activate Activator
}

type Deps struct {
	Wallets  Wallets
	Accounts Accounts
	Chains   Chains
	Activate Activator
}

func NewService(deps Deps) *Service {
	if deps.Wallets == nil || deps.Accounts == nil || deps.Chains == nil || deps.Activate == nil {
		panic("test wallet activation: all scoped stores and the activation service are required")
	}
	return &Service{wallets: deps.Wallets, accounts: deps.Accounts, chains: deps.Chains, activate: deps.Activate}
}

type Input struct {
	AccountID     string
	WalletID      string
	ExpectedChain string
	Apply         bool
}

// Report never carries a model, activation credential or key material.
type Report struct {
	AccountID string
	WalletID  string
	Chain     string
	Status    string
	Applied   bool
}

var chainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,49}$`)
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)

// Recover inspects by default and activates only the selected pending test wallet with explicit Apply.
func (s *Service) Recover(ctx context.Context, input Input) (Report, error) {
	accountID, accountErr := uuid.Parse(input.AccountID)
	walletID, walletErr := uuid.Parse(input.WalletID)
	if accountErr != nil || walletErr != nil || accountID == uuid.Nil || walletID == uuid.Nil ||
		input.ExpectedChain != strings.TrimSpace(input.ExpectedChain) || !chainPattern.MatchString(input.ExpectedChain) {
		return Report{}, ErrSelection
	}
	var report Report
	err := s.wallets.Within(ctx, func(writeCtx context.Context) error {
		// Fixed lock order also serializes network/account changes during this repair.
		chain, err := s.chains.LockByID(writeCtx, input.ExpectedChain)
		if err != nil || chain == nil || chain.ID != input.ExpectedChain || !chain.IsTestnet || chain.Status != models.StatusActive {
			return ErrBinding
		}
		account, err := s.accounts.LockByID(writeCtx, accountID)
		if err != nil || account == nil || account.ID != accountID || account.Environment != models.EnvironmentTest || account.Status != models.StatusActive {
			return ErrBinding
		}
		selected, err := s.wallets.LockByID(writeCtx, walletID)
		if err != nil || selected == nil || selected.ID != walletID || selected.AccountID == nil || *selected.AccountID != accountID ||
			selected.Chain != input.ExpectedChain || selected.Status != "pending" || selected.FrozenUntil != nil {
			return ErrBinding
		}
		if selected.ActivationCode == nil || !codePattern.MatchString(*selected.ActivationCode) {
			return ErrCodeUnavailable
		}
		report = Report{AccountID: accountID.String(), WalletID: walletID.String(), Chain: selected.Chain, Status: selected.Status}
		if !input.Apply {
			return nil
		}
		// The credential never crosses a command argument, report, log, cache or HTTP boundary.
		activated, err := s.activate.ActivateWallet(writeCtx, walletID, *selected.ActivationCode)
		if err != nil || activated == nil || activated.ID != walletID || activated.AccountID == nil || *activated.AccountID != accountID ||
			activated.Chain != input.ExpectedChain || activated.Status != "active" || activated.ActivationCode != nil {
			return ErrActivation
		}
		report.Status = activated.Status
		report.Applied = true
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return report, nil
}
