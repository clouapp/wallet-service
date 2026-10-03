package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestInviteTokenCanBeAcceptedWithoutAnEmptyPassword(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "invite-account")
	ownerID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'owner-invite@example.com', 'hash', 'Owner', 'active', NOW(), NOW())`, ownerID)
	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'owner', 'active', NOW(), NOW())`, uuid.New(), account.ID, ownerID)

	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
		Users:       repositories.NewUserRepository(nil),
		Invites:     repositories.NewAccountInviteRepository(nil),
		Activity:    repositories.NewAccountActivityRepository(nil),
	})
	issued, err := svc.IssueInvite(context.Background(), account.ID, "new-invite@example.com", models.AccountRoleAuditor, ownerID, "http://localhost:2001")
	require.NoError(t, err)
	require.NotContains(t, issued.InviteLink, "vault.app/accept-invite")
	require.Contains(t, issued.InviteLink, "token=")
	require.NotEqual(t, issued.RawToken, issued.Invite.TokenHash)
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM users WHERE email = 'new-invite@example.com'`))

	user, err := svc.AcceptInvite(context.Background(), issued.RawToken, "long-enough-password", "New Person", nil)
	require.NoError(t, err)
	require.Equal(t, "active", user.Status)
	require.NotEmpty(t, user.PasswordHash)
	require.NotEqual(t, "", user.PasswordHash)
	require.Equal(t, "auditor", scalar[string](t, `SELECT role FROM account_users WHERE user_id = ? AND deleted_at IS NULL`, user.ID))

	_, err = svc.AcceptInvite(context.Background(), issued.RawToken, "long-enough-password", "New Person", nil)
	require.ErrorIs(t, err, accountsvc.ErrInviteInvalid)

	require.Less(t, time.Until(issued.Invite.ExpiresAt), 73*time.Hour)
	require.False(t, strings.Contains(issued.Invite.TokenHash, issued.RawToken))

	invitedMeta := scalar[string](t, `SELECT metadata::text FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID)
	acceptedMeta := scalar[string](t, `SELECT metadata::text FROM account_activity WHERE action = 'invite.accepted' AND account_id = ?`, account.ID)
	require.Equal(t, "auditor", scalar[string](t, `SELECT metadata->>'role' FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID))
	require.Equal(t, "auditor", scalar[string](t, `SELECT metadata->>'role' FROM account_activity WHERE action = 'invite.accepted' AND account_id = ?`, account.ID))
	require.NotContains(t, invitedMeta, issued.RawToken)
	require.NotContains(t, acceptedMeta, issued.RawToken)
	require.NotContains(t, invitedMeta, issued.Invite.TokenHash)
	require.NotContains(t, acceptedMeta, issued.Invite.TokenHash)
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE account_id IS NULL AND action IN ('member.invited', 'invite.accepted')`))
}

type refuseActivity struct {
	inner *repositories.AccountActivityRepository
}

func (r refuseActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return r.inner.Within(ctx, fn)
}

func (r refuseActivity) Append(context.Context, models.AccountActivity) error {
	return errors.New("activity refused")
}

func TestInviteActivityRollsBackWithTheInvite(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "invite-rollback")
	ownerID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'owner-rollback@example.com', 'hash', 'Owner', 'active', NOW(), NOW())`, ownerID)
	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'owner', 'active', NOW(), NOW())`, uuid.New(), account.ID, ownerID)

	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
		Users:       repositories.NewUserRepository(nil),
		Invites:     repositories.NewAccountInviteRepository(nil),
		Activity:    refuseActivity{inner: repositories.NewAccountActivityRepository(nil)},
	})

	_, err := svc.IssueInvite(context.Background(), account.ID, "rolled-back@example.com", models.AccountRoleUser, ownerID, "http://localhost:2001")
	require.Error(t, err)
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE account_id = ?`, account.ID))
	require.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE account_id = ?`, account.ID))
}
