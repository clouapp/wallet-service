package policies

import (
	"testing"

	"github.com/google/uuid"
)

func TestWallet_Decisions_FollowTheLoadedRoles(t *testing.T) {
	t.Parallel()

	owner := WalletMembership{WalletRole: roleOwner}
	admin := WalletMembership{WalletRole: roleAdmin}
	viewer := WalletMembership{WalletRole: "viewer"}
	accountOwner := WalletMembership{AccountRole: roleOwner}
	accountAdmin := WalletMembership{AccountRole: roleAdmin}
	accountAuditor := WalletMembership{AccountRole: roleAuditor}
	accountUser := WalletMembership{AccountRole: roleUser}
	empty := WalletMembership{}

	for _, membership := range []WalletMembership{owner, admin, accountOwner, accountAdmin} {
		if !WalletUpdate(membership).Allowed() || !WalletAddUser(membership).Allowed() || !WalletRemoveUser(membership).Allowed() || !WalletWhitelist(membership).Allowed() || !WalletManageWebhooks(membership).Allowed() {
			t.Fatalf("owner and admin memberships must administer: %+v", membership)
		}
	}
	for _, membership := range []WalletMembership{viewer, accountAuditor, accountUser, empty} {
		if WalletUpdate(membership).Allowed() || WalletUpdate(membership).Message() != "only wallet/account owners and admins may update wallet settings" {
			t.Fatalf("non-admin membership must not update: %+v", membership)
		}
		if WalletAddUser(membership).Message() != "only wallet/account owners and admins may add wallet users" {
			t.Fatal("add-user denial message changed")
		}
		if WalletWhitelist(membership).Message() != "only wallet/account owners and admins may manage the whitelist" {
			t.Fatal("whitelist denial message changed")
		}
		if WalletManageWebhooks(membership).Message() != "only wallet/account owners and admins may manage webhooks" {
			t.Fatal("webhook denial message changed")
		}
	}

	if !WalletFreeze(owner).Allowed() || !WalletFreeze(accountOwner).Allowed() || !WalletFreeze(accountAdmin).Allowed() {
		t.Fatal("wallet owner and account owner or admin may freeze")
	}
	if WalletFreeze(admin).Allowed() || WalletFreeze(admin).Message() != "only owners and account admins may freeze wallets" {
		t.Fatal("a wallet admin may not freeze")
	}
	if WalletFreeze(accountAuditor).Allowed() || WalletFreeze(viewer).Allowed() {
		t.Fatal("auditor and viewer may not freeze")
	}
}

func TestWallet_Cancel_WithdrawalKeepsTheCreatorRule(t *testing.T) {
	t.Parallel()

	creator := uuid.New()
	other := uuid.New()
	viewer := WalletMembership{WalletRole: "viewer", UserID: creator}
	if !WalletCancelWithdrawal(viewer, creator).Allowed() {
		t.Fatal("the creator may cancel without an admin role")
	}
	auditor := WalletMembership{AccountRole: roleAuditor, UserID: creator}
	if decision := WalletCancelWithdrawal(auditor, creator); decision.Allowed() || decision.Message() != "only the creator or an owner/admin may cancel this withdrawal" {
		t.Fatal("an auditor may not cancel a withdrawal they created")
	}
	if WalletCancelWithdrawal(viewer, other).Allowed() || WalletCancelWithdrawal(viewer, other).Message() != "only the creator or an owner/admin may cancel this withdrawal" {
		t.Fatal("another user may not cancel")
	}
	if !WalletCancelWithdrawal(WalletMembership{WalletRole: roleAdmin, UserID: other}, creator).Allowed() {
		t.Fatal("a wallet admin may cancel someone else's withdrawal")
	}
	if !WalletCancelWithdrawal(WalletMembership{AccountRole: roleOwner, UserID: other}, creator).Allowed() {
		t.Fatal("an account owner may cancel someone else's withdrawal")
	}
}

func TestWallet_Cancel_WithdrawalDeniesNilAndEmptyRoleSets(t *testing.T) {
	t.Parallel()

	const denied = "only the creator or an owner/admin may cancel this withdrawal"
	if decision := WalletCancelWithdrawal(WalletMembership{}, uuid.Nil); decision.Allowed() || decision.Message() != denied {
		t.Fatal("a nil role set may not cancel")
	}
	creator := uuid.New()
	empty := WalletMembership{WalletRole: "", AccountRole: "", UserID: creator}
	if decision := WalletCancelWithdrawal(empty, creator); decision.Allowed() || decision.Message() != denied {
		t.Fatal("an empty role set may not cancel")
	}
	if !WalletCancelWithdrawal(WalletMembership{WalletRole: "viewer", UserID: creator}, creator).Allowed() {
		t.Fatal("a viewer who created the withdrawal may still cancel")
	}
}
