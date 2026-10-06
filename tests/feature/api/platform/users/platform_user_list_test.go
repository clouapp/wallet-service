package users

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const (
	passwordHashMarker     = "password-hash-marker"
	totpSecretMarker       = "totp-secret-marker"
	recoveryCodeHashMarker = "recovery-code-hash-marker"
	refreshTokenHashMarker = "refresh-token-hash-marker"
	suspensionReasonMarker = "suspension-reason-marker"
)

// PlatformUserListTestSuite is GET /v1/platform/users from S3.4.1.
// users.view is the named permission; a platform_admins row is the gate.
type PlatformUserListTestSuite struct {
	authSuite
}

func TestPlatform_User_ListSuite(t *testing.T) {
	suite.Run(t, new(PlatformUserListTestSuite))
}

func (s *PlatformUserListTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformUserListTestSuite) TestA_Platform_AdminListsUsersOrderedByCreatedAtDescending() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	s.stampProfile(admin.ID, "2025-01-01 00:00:00", "Admin Ada", false, false)

	older := s.seedUser(false)
	s.stampProfile(older.ID, "2024-01-01 00:00:00", "Older Olive", false, false)

	middle := s.seedUser(false)
	s.stampProfile(middle.ID, "2024-06-01 00:00:00", "Middle Mia", true, true)
	s.plantSecrets(middle.ID)

	session := s.signIn(admin.Email)
	page := s.listUsers(session.AccessToken, "")
	page.AssertOk()
	body := s.listed(page)
	s.Equal(int64(3), body.Total)
	s.Equal(20, body.Limit)
	s.Equal(0, body.Offset)
	s.Require().Len(body.Data, 3)
	s.Equal([]string{admin.ID.String(), middle.ID.String(), older.ID.String()}, idsOf(body.Data))

	s.Equal("Admin Ada", body.Data[0].FullName)
	s.Equal("active", body.Data[0].Status)
	s.Nil(body.Data[0].SuspendedAt)
	s.False(body.Data[0].TotpEnabled)

	s.Equal("Middle Mia", body.Data[1].FullName)
	s.Equal("active", body.Data[1].Status)
	s.Require().NotNil(body.Data[1].SuspendedAt)
	s.Equal("2026-10-01T12:00:00Z", *body.Data[1].SuspendedAt)
	s.True(body.Data[1].TotpEnabled)

	s.Equal("Older Olive", body.Data[2].FullName)
	s.False(body.Data[2].TotpEnabled)
	s.Nil(body.Data[2].SuspendedAt)

	raw, err := page.Content()
	s.Require().NoError(err)
	s.assertSafeUserList(raw)

	newest := s.listUsers(session.AccessToken, "?limit=1&offset=0")
	newest.AssertOk()
	first := s.listed(newest)
	s.Equal(1, first.Limit)
	s.Equal(0, first.Offset)
	s.Equal(int64(3), first.Total)
	s.Require().Len(first.Data, 1)
	s.Equal(admin.ID.String(), first.Data[0].ID)

	skipped := s.listUsers(session.AccessToken, "?limit=1&offset=1")
	skipped.AssertOk()
	second := s.listed(skipped)
	s.Equal(1, second.Offset)
	s.Require().Len(second.Data, 1)
	s.Equal(middle.ID.String(), second.Data[0].ID)
}

func (s *PlatformUserListTestSuite) TestA_Member_CannotListPlatformUsers() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)

	resp := s.listUsers(session.AccessToken, "")
	resp.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to view users", body.Error.Message)

	anonymous, err := s.Http(s.T()).Get("/v1/platform/users")
	s.Require().NoError(err)
	anonymous.AssertUnauthorized()
}

func (s *PlatformUserListTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserListTestSuite) stampProfile(id uuid.UUID, createdAt, fullName string, suspended, totp bool) {
	s.T().Helper()
	suspendedAt := any(nil)
	if suspended {
		suspendedAt = "2026-10-01 12:00:00+00"
	}
	_, err := facades.Orm().Query().Exec(
		`UPDATE users
		 SET created_at = ?, full_name = ?, suspended_at = ?, totp_enabled = ?
		 WHERE id = ?`,
		createdAt, fullName, suspendedAt, totp, id,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserListTestSuite) plantSecrets(id uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`UPDATE users
		 SET password_hash = ?, suspension_reason = ?, sessions_revoked_at = '2026-10-02 12:00:00+00'
		 WHERE id = ?`,
		passwordHashMarker, suspensionReasonMarker, id,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, ?, 0, NOW(), NOW())`,
		uuid.New(), id, totpSecretMarker,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_backup_codes (
			id, subject_type, subject_id, code_hash, created_at, updated_at
		) VALUES (?, 'users', ?, ?, NOW(), NOW())`,
		uuid.New(), id, recoveryCodeHashMarker,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, NOW() + INTERVAL '1 day', NOW(), NOW())`,
		uuid.New(), id, refreshTokenHashMarker,
	)
	s.Require().NoError(err)
}

func (s *PlatformUserListTestSuite) listUsers(bearer, query string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		Get("/v1/platform/users" + query)
	s.Require().NoError(err)
	return resp
}

type listedUsers struct {
	Data   []listedUser `json:"data"`
	Total  int64        `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

type listedUser struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	FullName    string  `json:"full_name"`
	Status      string  `json:"status"`
	SuspendedAt *string `json:"suspended_at"`
	TotpEnabled bool    `json:"totp_enabled"`
}

func (s *PlatformUserListTestSuite) listed(resp contractstesting.Response) listedUsers {
	s.T().Helper()
	var body listedUsers
	s.decode(resp, &body)
	return body
}

func idsOf(rows []listedUser) []string {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	return ids
}

func (s *PlatformUserListTestSuite) assertSafeUserList(raw string) {
	s.T().Helper()
	for _, marker := range []string{
		passwordHashMarker,
		totpSecretMarker,
		recoveryCodeHashMarker,
		refreshTokenHashMarker,
		suspensionReasonMarker,
	} {
		s.NotContains(raw, marker)
	}
	var payload any
	s.Require().NoError(json.Unmarshal([]byte(raw), &payload))
	s.assertExactKeys(payload, "data", "limit", "offset", "total")
	object, ok := payload.(map[string]any)
	s.Require().True(ok)
	s.walkForSecretKeys(object["data"])
	rows, ok := object["data"].([]any)
	s.Require().True(ok)
	for _, row := range rows {
		s.assertExactKeys(row, "email", "full_name", "id", "status", "suspended_at", "totp_enabled")
	}
	s.Contains(raw, `"totp_enabled":true`)
	s.Contains(raw, `"totp_enabled":false`)
}

func (s *PlatformUserListTestSuite) assertExactKeys(node any, keys ...string) {
	s.T().Helper()
	object, ok := node.(map[string]any)
	s.Require().True(ok)
	s.Require().Len(object, len(keys))
	for _, key := range keys {
		_, present := object[key]
		s.True(present, key)
	}
}

func (s *PlatformUserListTestSuite) walkForSecretKeys(node any) {
	s.T().Helper()
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			lower := strings.ToLower(key)
			for _, forbidden := range []string{
				"password", "password_hash", "totp_secret", "secret", "recovery",
				"code_hash", "refresh_token", "access_token", "token_hash",
				"sessions_revoked_at", "suspension_reason", "preferences",
			} {
				s.NotEqual(forbidden, lower)
			}
			s.walkForSecretKeys(child)
		}
	case []any:
		for _, child := range value {
			s.walkForSecretKeys(child)
		}
	}
}
