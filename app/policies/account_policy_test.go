package policies

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/app/models"
)

type stubAccountUsers struct {
	t         *testing.T
	accountID uuid.UUID
	userID    uuid.UUID
	found     *models.AccountUser
	err       error
}

func (s stubAccountUsers) FindByAccountAndUser(_ context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	if s.t != nil && (accountID != s.accountID || userID != s.userID) {
		s.t.Errorf("lookup account %s user %s, want account %s user %s", accountID, userID, s.accountID, s.userID)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.found, nil
}

func bindAccountUsers(t *testing.T, repo accountMemberships) {
	t.Helper()
	previous := accountUsers
	accountUsers = repo
	t.Cleanup(func() { accountUsers = previous })
}

func TestAccountDecisionsFollowTheLoadedMembership(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()

	cases := []struct {
		role   string
		decide func(context.Context, uuid.UUID, uuid.UUID) contractsaccess.Response
		allow  bool
		deny   string
	}{
		{roleOwner, AccountUpdate, true, ""},
		{roleAdmin, AccountUpdate, true, ""},
		{roleAuditor, AccountUpdate, false, "only owners and admins may update account settings"},
		{roleUser, AccountUpdate, false, "only owners and admins may update account settings"},
		{roleOwner, AccountAddUser, true, ""},
		{roleAdmin, AccountAddUser, true, ""},
		{roleAuditor, AccountAddUser, false, "only owners and admins may add users"},
		{roleOwner, AccountRemoveUser, true, ""},
		{roleUser, AccountRemoveUser, false, "only owners and admins may remove users"},
		{roleOwner, AccountManageTokens, true, ""},
		{roleAdmin, AccountManageTokens, true, ""},
		{roleAuditor, AccountManageTokens, false, "only owners and admins may manage tokens"},
		{roleOwner, AccountFreeze, true, ""},
		{roleAdmin, AccountFreeze, false, "only owners may freeze accounts"},
		{roleOwner, AccountArchive, true, ""},
		{roleAdmin, AccountArchive, false, "only owners may archive accounts"},
	}

	for _, tc := range cases {
		bindAccountUsers(t, stubAccountUsers{t: t, accountID: accountID, userID: userID, found: &models.AccountUser{Role: tc.role}})
		decision := tc.decide(ctx, accountID, userID)
		if decision.Allowed() != tc.allow {
			t.Fatalf("role %s allowed=%v, want %v", tc.role, decision.Allowed(), tc.allow)
		}
		if !tc.allow && decision.Message() != tc.deny {
			t.Fatalf("role %s denial %q, want %q", tc.role, decision.Message(), tc.deny)
		}
	}
}

func TestAccountViewAllowsAnyStoredRole(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	policy := &AccountPolicy{}
	ctx := context.Background()

	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser} {
		bindAccountUsers(t, stubAccountUsers{t: t, accountID: accountID, userID: userID, found: &models.AccountUser{Role: role}})
		decision := policy.View(ctx, map[string]any{"account_id": accountID, "user_id": userID})
		if !decision.Allowed() {
			t.Fatalf("role %s must be allowed to view", role)
		}
	}

	bindAccountUsers(t, stubAccountUsers{t: t, accountID: accountID, userID: userID})
	missing := policy.View(ctx, map[string]any{"account_id": accountID, "user_id": userID})
	if missing.Allowed() || missing.Message() != "not a member of this account" {
		t.Fatalf("a missing membership must deny view, got allowed=%v message=%q", missing.Allowed(), missing.Message())
	}
}

func TestAccountPolicyKeepsTheMissingCallerDeny(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	ctx := context.Background()
	decision := AccountUpdate(ctx, accountID, uuid.Nil)
	if decision.Allowed() || decision.Message() != "only owners and admins may update account settings" {
		t.Fatalf("a missing caller must deny update, got allowed=%v message=%q", decision.Allowed(), decision.Message())
	}

	policy := &AccountPolicy{}
	for _, decide := range []func(context.Context, map[string]any) contractsaccess.Response{
		policy.View,
		policy.Update,
		policy.Delete,
		policy.AddUser,
		policy.RemoveUser,
		policy.Freeze,
		policy.Archive,
		policy.ManageTokens,
	} {
		got := decide(ctx, map[string]any{"user_id": uuid.New()})
		if got.Allowed() || got.Message() != "missing account_id" {
			t.Fatalf("gate without account_id allowed or changed the message: %q", got.Message())
		}
	}
}

func TestAccountPolicyTreatsALookupErrorAsNoMembership(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	bindAccountUsers(t, stubAccountUsers{t: t, accountID: accountID, userID: userID, err: errors.New("store down")})

	decision := (&AccountPolicy{}).Delete(context.Background(), accountDecisionArguments(accountID, userID))
	if decision.Allowed() || decision.Message() != "only owners may delete accounts" {
		t.Fatalf("a lookup error must deny delete, got allowed=%v message=%q", decision.Allowed(), decision.Message())
	}
}
