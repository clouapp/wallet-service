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

func TestResendInviteRotatesTheOpenToken(t *testing.T) {
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "invite-resend")
	other := mocks.InsertAccount(t, "invite-resend-other")
	ownerID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'owner-resend@example.com', 'hash', 'Owner', 'active', NOW(), NOW())`, ownerID)
	exec(t, `INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'owner', 'active', NOW(), NOW())`, uuid.New(), account.ID, ownerID)

	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
		Users:       repositories.NewUserRepository(nil),
		Invites:     repositories.NewAccountInviteRepository(nil),
		Activity:    repositories.NewAccountActivityRepository(nil),
	})
	const frontend = "http://localhost:2001"
	issued, err := svc.IssueInvite(context.Background(), account.ID, "resend-me@example.com", models.AccountRoleAuditor, ownerID, frontend)
	require.NoError(t, err)
	invited := scalar[int64](t, `SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID)
	require.Equal(t, int64(1), invited)
	expiresBefore := scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID)

	_, err = svc.ResendInvite(nil, account.ID, issued.Invite.ID, frontend)
	require.Error(t, err)
	_, err = svc.ResendInvite(context.Background(), uuid.Nil, issued.Invite.ID, frontend)
	require.ErrorIs(t, err, accountsvc.ErrInviteInvalid)

	resent, err := svc.ResendInvite(context.Background(), account.ID, issued.Invite.ID, frontend)
	require.NoError(t, err)
	require.NotEqual(t, issued.RawToken, resent.RawToken)
	require.Equal(t, models.AccountRoleAuditor, resent.Invite.Role)
	require.Equal(t, accountsvc.HashInviteToken(resent.RawToken), scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))
	require.Contains(t, resent.InviteLink, resent.RawToken)
	require.NotContains(t, resent.InviteLink, resent.Invite.TokenHash)
	require.Equal(t, expiresBefore, scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID))
	require.Equal(t, invited, scalar[int64](t, `SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID))

	_, _, err = svc.PreviewInvite(context.Background(), issued.RawToken)
	require.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	preview, needsPassword, err := svc.PreviewInvite(context.Background(), resent.RawToken)
	require.NoError(t, err)
	require.True(t, needsPassword)
	require.Equal(t, "resend-me@example.com", preview.Email)
	require.Equal(t, models.AccountRoleAuditor, preview.Role)

	_, err = svc.ResendInvite(context.Background(), other.ID, issued.Invite.ID, frontend)
	require.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	require.Equal(t, accountsvc.HashInviteToken(resent.RawToken), scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))

	_, err = svc.AcceptInvite(context.Background(), resent.RawToken, "long-enough-password", "Resent Person", nil)
	require.NoError(t, err)
	hashAfterAccept := scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID)
	_, err = svc.ResendInvite(context.Background(), account.ID, issued.Invite.ID, frontend)
	require.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	require.Equal(t, hashAfterAccept, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))
}
