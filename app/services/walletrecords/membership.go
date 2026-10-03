package walletrecords

import (
	"context"

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

// NewMemberships builds the loader the wallet policy's callers use.
func NewMemberships(wallets *Wallets, members *Members, accounts AccountMemberLookup) *Memberships {
	if wallets == nil || members == nil || accounts == nil {
		panic("wallet memberships: wallets, members, and account memberships are required")
	}
	return &Memberships{wallets: wallets, members: members, accounts: accounts}
}

// ForWallet returns the caller's wallet role and account role.
// A missing row or a lookup error is an empty role, the same answer the
// wallet policy used when it queried the repositories itself.
func (m *Memberships) ForWallet(ctx context.Context, walletID, userID uuid.UUID) (walletRole, accountRole string) {
	if m == nil || m.members == nil || m.wallets == nil || m.accounts == nil {
		return "", ""
	}
	member, err := m.members.FindByWalletAndUser(ctx, walletID, userID)
	if err == nil && member != nil {
		walletRole = member.Roles
	}
	wallet, err := m.wallets.FindByID(ctx, walletID)
	if err != nil || wallet == nil || wallet.AccountID == nil {
		return walletRole, ""
	}
	accountMember, err := m.accounts.FindMember(ctx, *wallet.AccountID, userID)
	if err != nil || accountMember == nil {
		return walletRole, ""
	}
	return walletRole, accountMember.Role
}
