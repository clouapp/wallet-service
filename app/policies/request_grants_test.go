package policies

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRequest_Grants_UseTheStoredRole(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	ctx := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleUser))
	update := AccountUpdate(ctx, accountID, userID)
	if update.Allowed() || update.Message() != "only owners and admins may update account settings" {
		t.Fatalf("user update allowed=%v message=%q", update.Allowed(), update.Message())
	}
	tokens := AccountReadTokens(ctx, accountID, userID)
	if tokens.Allowed() || tokens.Message() != "only owners, admins, and auditors may read tokens" {
		t.Fatalf("user tokens allowed=%v message=%q", tokens.Allowed(), tokens.Message())
	}
	wallet, ok := WalletRequestGrants(ctx)
	if !ok || !Can(wallet, PermAddressesCreate) || Can(wallet, PermWithdrawalsCreate) || Can(wallet, PermSweepExecute) || Can(wallet, PermWalletsCreate) {
		t.Fatalf("user wallet grants = %v ok=%v", wallet, ok)
	}
	account, ok := AccountGrants(ctx)
	if !ok || Can(account, PermWithdrawalsCreate) || Can(account, PermUsersRead) {
		t.Fatalf("user account grants = %v ok=%v", account, ok)
	}

	second := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleUser))
	again := AccountUpdate(second, accountID, userID)
	if again.Allowed() {
		t.Fatal("user must still be refused on the next request")
	}

	empty := context.WithValue(context.Background(), RequestGrantsKey(), emptyRequestGrants())
	unloaded := AccountUpdate(empty, accountID, userID)
	if unloaded.Allowed() || unloaded.Message() != "only owners and admins may update account settings" {
		t.Fatalf("an unloaded grant allowed=%v message=%q", unloaded.Allowed(), unloaded.Message())
	}
	if _, ok := AccountGrants(empty); ok {
		t.Fatal("an unloaded grant stays unloaded")
	}
}

func TestPrepared_Request_GrantsSkipTheMembershipQuery(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()

	ctx := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleUser))
	if AccountUpdate(ctx, accountID, userID).Allowed() || AccountReadTokens(ctx, accountID, userID).Allowed() {
		t.Fatal("a prepared user grant must still refuse update and token reads")
	}
}

func TestWallet_Policy_ReadsRequestGrants(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	walletID := uuid.New()

	ctx := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleUser))
	policy := &WalletPolicy{}
	view := policy.View(ctx, map[string]any{"wallet_id": walletID, "user_id": userID})
	if !view.Allowed() {
		t.Fatal("a user grant may view the wallet")
	}
	update := policy.Update(ctx, map[string]any{"wallet_id": walletID, "user_id": userID})
	if update.Allowed() || update.Message() != "only wallet/account owners and admins may update wallet settings" {
		t.Fatalf("user grant update allowed=%v message=%q", update.Allowed(), update.Message())
	}
	again := policy.Whitelist(ctx, map[string]any{"wallet_id": walletID, "user_id": userID})
	if again.Allowed() {
		t.Fatal("a second wallet check must still refuse the user")
	}

	explicit := policy.Update(ctx, map[string]any{
		"wallet_id":    walletID,
		"account_role": "",
	})
	if explicit.Allowed() {
		t.Fatal("an explicit empty account role stays empty")
	}
	owner := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleOwner))
	if policy.Update(owner, map[string]any{"wallet_id": walletID, "account_role": ""}).Allowed() {
		t.Fatal("an explicit empty account role is not replaced by an owner grant")
	}
}
