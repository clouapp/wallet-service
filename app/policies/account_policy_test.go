package policies

import (
	"context"
	"testing"

	"github.com/google/uuid"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

func withStoredAccountRole(accountID, userID uuid.UUID, role string) context.Context {
	return context.WithValue(context.Background(), RequestGrantsKey(), AttachRequestGrants(accountID, userID, role))
}

func TestAccount_Decisions_FollowTheLoadedMembership(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()

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
		{roleOwner, AccountReadTokens, true, ""},
		{roleAdmin, AccountReadTokens, true, ""},
		{roleAuditor, AccountReadTokens, true, ""},
		{roleUser, AccountReadTokens, false, "only owners, admins, and auditors may read tokens"},
		{roleOwner, AccountWriteTokens, true, ""},
		{roleAdmin, AccountWriteTokens, true, ""},
		{roleAuditor, AccountWriteTokens, false, "only owners and admins may manage tokens"},
		{roleUser, AccountWriteTokens, false, "only owners and admins may manage tokens"},
		{roleOwner, AccountFreeze, true, ""},
		{roleAdmin, AccountFreeze, false, "only owners may freeze accounts"},
		{roleOwner, AccountArchive, true, ""},
		{roleAdmin, AccountArchive, false, "only owners may archive accounts"},
	}

	for _, tc := range cases {
		decision := tc.decide(withStoredAccountRole(accountID, userID, tc.role), accountID, userID)
		if decision.Allowed() != tc.allow {
			t.Fatalf("role %s allowed=%v, want %v", tc.role, decision.Allowed(), tc.allow)
		}
		if !tc.allow && decision.Message() != tc.deny {
			t.Fatalf("role %s denial %q, want %q", tc.role, decision.Message(), tc.deny)
		}
	}
}

func TestAccount_View_AllowsAnyStoredRole(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	policy := &AccountPolicy{}

	for _, role := range []string{roleOwner, roleAdmin, roleAuditor, roleUser} {
		decision := policy.View(withStoredAccountRole(accountID, userID, role), map[string]any{"account_id": accountID, "user_id": userID})
		if !decision.Allowed() {
			t.Fatalf("role %s must be allowed to view", role)
		}
	}

	missing := policy.View(context.Background(), map[string]any{"account_id": accountID, "user_id": userID})
	if missing.Allowed() || missing.Message() != "not a member of this account" {
		t.Fatalf("a missing membership must deny view, got allowed=%v message=%q", missing.Allowed(), missing.Message())
	}
}

func TestAccount_Policy_KeepsTheMissingCallerDeny(t *testing.T) {
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
		policy.ReadTokens,
		policy.WriteTokens,
	} {
		got := decide(ctx, map[string]any{"user_id": uuid.New()})
		if got.Allowed() || got.Message() != "missing account_id" {
			t.Fatalf("gate without account_id allowed or changed the message: %q", got.Message())
		}
	}
}

func TestAccount_Policy_UsesThePassedAccountRole(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()
	policy := &AccountPolicy{}

	owner := policy.Update(context.Background(), map[string]any{
		"account_id":   accountID,
		"user_id":      userID,
		"account_role": roleOwner,
	})
	if !owner.Allowed() {
		t.Fatal("a passed owner role may update the account")
	}

	user := policy.Update(context.Background(), map[string]any{
		"account_id":   accountID,
		"user_id":      userID,
		"account_role": roleUser,
	})
	if user.Allowed() || user.Message() != "only owners and admins may update account settings" {
		t.Fatalf("a passed user role allowed=%v message=%q", user.Allowed(), user.Message())
	}

	storedOwner := withStoredAccountRole(accountID, userID, roleOwner)
	explicitEmpty := policy.Update(storedOwner, map[string]any{
		"account_id":   accountID,
		"user_id":      userID,
		"account_role": "",
	})
	if explicitEmpty.Allowed() {
		t.Fatal("an explicit empty account role stays empty")
	}

	otherUser := uuid.New()
	mismatch := AccountUpdate(withStoredAccountRole(accountID, otherUser, roleOwner), accountID, userID)
	if mismatch.Allowed() || mismatch.Message() != "only owners and admins may update account settings" {
		t.Fatalf("another user's stored role allowed=%v message=%q", mismatch.Allowed(), mismatch.Message())
	}
}

func TestAccount_Policy_TreatsAMissingStoredRoleAsNoMembership(t *testing.T) {
	accountID := uuid.New()
	userID := uuid.New()

	decision := (&AccountPolicy{}).Delete(context.Background(), accountDecisionArguments(accountID, userID))
	if decision.Allowed() || decision.Message() != "only owners may delete accounts" {
		t.Fatalf("a lookup error must deny delete, got allowed=%v message=%q", decision.Allowed(), decision.Message())
	}
}
