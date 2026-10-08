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
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/assert"
)

func TestInvite_Token_CanBeAcceptedWithoutAnEmptyPassword(t *testing.T) {
	fixtures.TestDB(t)
	account := fixtures.InsertAccount(t, "invite-account")
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
	assert.NotContains(t, issued.InviteLink, "vault.app/accept-invite")
	assert.Contains(t, issued.InviteLink, "token=")
	assert.NotEqual(t, issued.RawToken, issued.Invite.TokenHash)
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM users WHERE email = 'new-invite@example.com'`))

	user, err := svc.AcceptInvite(context.Background(), issued.RawToken, "long-enough-password", "New Person", nil)
	require.NoError(t, err)
	assert.Equal(t, "active", user.Status)
	require.NotEmpty(t, user.PasswordHash)
	assert.NotEqual(t, "", user.PasswordHash)
	assert.Equal(t, "auditor", scalar[string](t, `SELECT role FROM account_users WHERE user_id = ? AND deleted_at IS NULL`, user.ID))

	_, err = svc.AcceptInvite(context.Background(), issued.RawToken, "long-enough-password", "New Person", nil)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)

	assert.Less(t, time.Until(issued.Invite.ExpiresAt), 73*time.Hour)
	assert.False(t, strings.Contains(issued.Invite.TokenHash, issued.RawToken))

	invitedMeta := scalar[string](t, `SELECT metadata::text FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID)
	acceptedMeta := scalar[string](t, `SELECT metadata::text FROM account_activity WHERE action = 'invite.accepted' AND account_id = ?`, account.ID)
	assert.Equal(t, "auditor", scalar[string](t, `SELECT metadata->>'role' FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID))
	assert.Equal(t, "auditor", scalar[string](t, `SELECT metadata->>'role' FROM account_activity WHERE action = 'invite.accepted' AND account_id = ?`, account.ID))
	assert.NotContains(t, invitedMeta, issued.RawToken)
	assert.NotContains(t, acceptedMeta, issued.RawToken)
	assert.NotContains(t, invitedMeta, issued.Invite.TokenHash)
	assert.NotContains(t, acceptedMeta, issued.Invite.TokenHash)
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE account_id IS NULL AND action IN ('member.invited', 'invite.accepted')`))
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

func TestInvite_Activity_RollsBackWithTheInvite(t *testing.T) {
	fixtures.TestDB(t)
	account := fixtures.InsertAccount(t, "invite-rollback")
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
	assert.Error(t, err)
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE account_id = ?`, account.ID))
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_activity WHERE account_id = ?`, account.ID))
}

func TestResend_Invite_RotatesTheOpenToken(t *testing.T) {
	fixtures.TestDB(t)
	account := fixtures.InsertAccount(t, "invite-resend")
	other := fixtures.InsertAccount(t, "invite-resend-other")
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
	assert.Equal(t, int64(1), invited)
	expiresBefore := scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID)

	_, err = svc.ResendInvite(nil, account.ID, issued.Invite.ID, frontend)
	assert.Error(t, err)
	_, err = svc.ResendInvite(context.Background(), uuid.Nil, issued.Invite.ID, frontend)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)

	resent, err := svc.ResendInvite(context.Background(), account.ID, issued.Invite.ID, frontend)
	require.NoError(t, err)
	assert.NotEqual(t, issued.RawToken, resent.RawToken)
	assert.Equal(t, models.AccountRoleAuditor, resent.Invite.Role)
	assert.Equal(t, accountsvc.HashInviteToken(resent.RawToken), scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Contains(t, resent.InviteLink, resent.RawToken)
	assert.NotContains(t, resent.InviteLink, resent.Invite.TokenHash)
	assert.Equal(t, expiresBefore, scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Equal(t, invited, scalar[int64](t, `SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID))

	_, _, err = svc.PreviewInvite(context.Background(), issued.RawToken)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	preview, needsPassword, err := svc.PreviewInvite(context.Background(), resent.RawToken)
	require.NoError(t, err)
	assert.True(t, needsPassword)
	assert.Equal(t, "resend-me@example.com", preview.Email)
	assert.Equal(t, models.AccountRoleAuditor, preview.Role)

	_, err = svc.ResendInvite(context.Background(), other.ID, issued.Invite.ID, frontend)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	assert.Equal(t, accountsvc.HashInviteToken(resent.RawToken), scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))

	_, err = svc.AcceptInvite(context.Background(), resent.RawToken, "long-enough-password", "Resent Person", nil)
	require.NoError(t, err)
	hashAfterAccept := scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID)
	_, err = svc.ResendInvite(context.Background(), account.ID, issued.Invite.ID, frontend)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	assert.Equal(t, hashAfterAccept, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))
}

func TestRevoke_Invite_StampsRevokedAtAndLeavesTheToken(t *testing.T) {
	fixtures.TestDB(t)
	account := fixtures.InsertAccount(t, "invite-revoke")
	other := fixtures.InsertAccount(t, "invite-revoke-other")
	ownerID := uuid.New()
	exec(t, `INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		VALUES (?, 'owner-revoke@example.com', 'hash', 'Owner', 'active', NOW(), NOW())`, ownerID)
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
	issued, err := svc.IssueInvite(context.Background(), account.ID, "revoke-me@example.com", models.AccountRoleAuditor, ownerID, frontend)
	require.NoError(t, err)
	invited := scalar[int64](t, `SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID)
	hashBefore := scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID)
	expiresBefore := scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID)

	assert.Error(t, svc.RevokeInvite(nil, account.ID, issued.Invite.ID))
	assert.ErrorIs(t, svc.RevokeInvite(context.Background(), uuid.Nil, issued.Invite.ID), accountsvc.ErrInviteInvalid)
	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND revoked_at IS NULL`, issued.Invite.ID))

	require.NoError(t, svc.RevokeInvite(context.Background(), account.ID, issued.Invite.ID))
	assert.Equal(t, hashBefore, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Equal(t, models.AccountRoleAuditor, scalar[string](t, `SELECT role FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Equal(t, expiresBefore, scalar[int64](t, `SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Equal(t, int64(0), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND revoked_at IS NULL`, issued.Invite.ID))
	revokedAt := scalar[int64](t, `SELECT EXTRACT(EPOCH FROM revoked_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID)
	assert.NotZero(t, revokedAt)
	_, _, err = svc.PreviewInvite(context.Background(), issued.RawToken)
	assert.ErrorIs(t, err, accountsvc.ErrInviteInvalid)
	assert.Equal(t, invited, scalar[int64](t, `SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, account.ID))

	assert.ErrorIs(t, svc.RevokeInvite(context.Background(), account.ID, issued.Invite.ID), accountsvc.ErrInviteInvalid)
	assert.Equal(t, revokedAt, scalar[int64](t, `SELECT EXTRACT(EPOCH FROM revoked_at)::bigint FROM account_invites WHERE id = ?`, issued.Invite.ID))
	assert.Equal(t, hashBefore, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))

	assert.ErrorIs(t, svc.RevokeInvite(context.Background(), other.ID, issued.Invite.ID), accountsvc.ErrInviteInvalid)
	assert.Equal(t, hashBefore, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, issued.Invite.ID))

	accepted, err := svc.IssueInvite(context.Background(), account.ID, "revoke-accepted@example.com", models.AccountRoleUser, ownerID, frontend)
	require.NoError(t, err)
	_, err = svc.AcceptInvite(context.Background(), accepted.RawToken, "long-enough-password", "Accepted Person", nil)
	require.NoError(t, err)
	acceptedHash := scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, accepted.Invite.ID)
	assert.ErrorIs(t, svc.RevokeInvite(context.Background(), account.ID, accepted.Invite.ID), accountsvc.ErrInviteInvalid)
	assert.Equal(t, acceptedHash, scalar[string](t, `SELECT token_hash FROM account_invites WHERE id = ?`, accepted.Invite.ID))
	assert.Equal(t, int64(1), scalar[int64](t, `SELECT count(*) FROM account_invites WHERE id = ? AND revoked_at IS NULL`, accepted.Invite.ID))
}
