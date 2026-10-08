package walletrecords

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AccountMemberLookup is the account membership read the wallet policy used
// to perform itself. The account service already owns that read.
type AccountMemberLookup interface {
	FindMember(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error)
}

// Memberships loads the wallet role and the account role for one caller.
type Memberships struct {
	wallets  *Wallets
	members  *Members
	accounts AccountMemberLookup
}

// MembershipsDeps is everything the wallet-role loader needs. Wallets, members,
// and the account membership lookup are required.
type MembershipsDeps struct {
	Wallets  *Wallets
	Members  *Members
	Accounts AccountMemberLookup
}

// NewMemberships builds the loader the wallet policy's callers use.
func NewMemberships(deps MembershipsDeps) *Memberships {
	if deps.Wallets == nil || deps.Members == nil || deps.Accounts == nil {
		panic("wallet memberships: wallets, members, and account memberships are required")
	}
	return &Memberships{wallets: deps.Wallets, members: deps.Members, accounts: deps.Accounts}
}

// ForWallet returns the caller's wallet role and account role.
// A missing membership row is an empty role. A failed membership read returns
// no roles, so a caller that allows either role cannot admit on that failure.
func (m *Memberships) ForWallet(ctx context.Context, walletID, userID uuid.UUID) (walletRole, accountRole string) {
	if m == nil || m.members == nil || m.wallets == nil || m.accounts == nil {
		return "", ""
	}
	member, err := m.members.FindByWalletAndUser(ctx, walletID, userID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return "", ""
	}
	if err == nil && member != nil {
		walletRole = member.Roles
	}
	wallet, err := m.wallets.FindByID(ctx, walletID)
	if err != nil || wallet == nil || wallet.AccountID == nil {
		return walletRole, ""
	}
	accountMember, err := m.accounts.FindMember(ctx, *wallet.AccountID, userID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return "", ""
	}
	if err != nil || accountMember == nil {
		return walletRole, ""
	}
	return walletRole, accountMember.Role
}
