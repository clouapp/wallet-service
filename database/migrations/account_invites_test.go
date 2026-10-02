package migrations_test

import (
	"context"
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

	svc := accountsvc.NewService(repositories.NewAccountRepository(), repositories.NewAccountUserRepository(), repositories.NewAccessTokenRepository())
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
}
