package walletrecords

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type keptAccounts struct{}

func (keptAccounts) FindMember(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, nil
}

func TestNewMembershipsKeepsItsDependencies(t *testing.T) {
	t.Parallel()

	wallets := NewWallets(nil)
	members := NewMembers(nil)
	var accounts AccountMemberLookup = keptAccounts{}
	loader := NewMemberships(MembershipsDeps{
		Wallets:  wallets,
		Members:  members,
		Accounts: accounts,
	})
	if loader == nil || loader.wallets != wallets || loader.members != members || loader.accounts != accounts {
		t.Fatal("memberships did not keep their dependencies")
	}
}
