package policies

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type countingAccountUsers struct {
	inner accountMemberships
	calls *int
}

func (c countingAccountUsers) FindByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	if c.calls != nil {
		*c.calls++
	}
	return c.inner.FindByAccountAndUser(ctx, accountID, userID)
}

func TestRequestGrantsResolveTheRoleOncePerRequest(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	calls := 0
	bindAccountUsers(t, countingAccountUsers{
		calls: &calls,
		inner: stubAccountUsers{
			t:         t,
			accountID: accountID,
			userID:    userID,
			found:     &models.AccountUser{Role: roleUser},
		},
	})

	ctx := context.WithValue(context.Background(), RequestGrantsKey(), emptyRequestGrants())
	update := AccountUpdate(ctx, accountID, userID)
	if update.Allowed() || update.Message() != "only owners and admins may update account settings" {
		t.Fatalf("user update allowed=%v message=%q", update.Allowed(), update.Message())
	}
	tokens := AccountReadTokens(ctx, accountID, userID)
	if tokens.Allowed() || tokens.Message() != "only owners, admins, and auditors may read tokens" {
		t.Fatalf("user tokens allowed=%v message=%q", tokens.Allowed(), tokens.Message())
	}
	if calls != 1 {
		t.Fatalf("two checks in one request resolved the role %d times", calls)
	}
	wallet, ok := WalletRequestGrants(ctx)
	if !ok || !Can(wallet, PermAddressesCreate) || Can(wallet, PermWithdrawalsCreate) || Can(wallet, PermSweepExecute) || Can(wallet, PermWalletsCreate) {
		t.Fatalf("user wallet grants = %v ok=%v", wallet, ok)
	}
	account, ok := AccountGrants(ctx)
	if !ok || Can(account, PermWithdrawalsCreate) || Can(account, PermUsersRead) {
		t.Fatalf("user account grants = %v ok=%v", account, ok)
	}

	second := context.WithValue(context.Background(), RequestGrantsKey(), emptyRequestGrants())
	again := AccountUpdate(second, accountID, userID)
	if again.Allowed() {
		t.Fatal("user must still be refused on the next request")
	}
	if calls != 2 {
		t.Fatalf("a second request resolved the role %d times, want 2", calls)
	}
}

func TestPreparedRequestGrantsSkipTheMembershipQuery(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	calls := 0
	bindAccountUsers(t, countingAccountUsers{
		calls: &calls,
		inner: stubAccountUsers{t: t, accountID: accountID, userID: userID, found: &models.AccountUser{Role: roleUser}},
	})

	ctx := context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, roleUser))
	if AccountUpdate(ctx, accountID, userID).Allowed() || AccountReadTokens(ctx, accountID, userID).Allowed() {
		t.Fatal("a prepared user grant must still refuse update and token reads")
	}
	if calls != 0 {
		t.Fatalf("prepared grants queried %d times", calls)
	}
}

func TestWalletPolicyReadsRequestGrants(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	walletID := uuid.New()
	calls := 0
	bindAccountUsers(t, countingAccountUsers{
		calls: &calls,
		inner: stubAccountUsers{t: t, accountID: accountID, userID: userID, found: &models.AccountUser{Role: roleOwner}},
	})

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
	if calls != 0 {
		t.Fatalf("wallet checks queried the account membership %d times", calls)
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
