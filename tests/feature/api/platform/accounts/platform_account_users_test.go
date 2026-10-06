package accounts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformAccountUsersTestSuite is GET /v1/platform/accounts/{accountId}/users
// from S3.4.1. The plan names the route and not the fields, so the page
// matches GET /v1/platform/users and each row matches the account member list.
type PlatformAccountUsersTestSuite struct {
	authSuite
}

func TestPlatform_Account_UsersSuite(t *testing.T) {
	suite.Run(t, new(PlatformAccountUsersTestSuite))
}

func (s *PlatformAccountUsersTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountUsersTestSuite) TestA_Platform_AdminListsAccountUsersNewestUserFirst() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)

	accountID := s.seedAccountUsersAccount("listed")
	otherID := s.seedAccountUsersAccount("other")
	emptyID := s.seedAccountUsersAccount("empty")

	newer := s.seedAccountPerson("2025-01-01 00:00:00", "Newer Ned", false, false, "")
	high := s.seedAccountPersonAt(uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff"), "2024-06-01 00:00:00", "High Hannah", true, true, "")
	low := s.seedAccountPersonAt(uuid.MustParse("11111111-1111-4111-8111-111111111111"), "2024-06-01 00:00:00", "Low Leo", false, false, "")
	older := s.seedAccountPerson("2024-01-01 00:00:00", "Older Olive", false, false, "")
	outsider := s.seedAccountPerson("2026-01-01 00:00:00", "Outsider Otto", false, false, "")
	removed := s.seedAccountPerson("2026-02-01 00:00:00", "Removed Rita", true, true, "removed-")

	s.seedMembership(accountID, newer, "owner", models.StatusActive, "2020-01-01 00:00:00", uuid.Nil, "")
	s.seedMembership(accountID, high, "admin", models.MembershipStatusSuspended, "2020-02-01 00:00:00", admin.ID, "")
	s.seedMembership(accountID, low, "user", models.StatusActive, "2026-01-01 00:00:00", uuid.Nil, "")
	s.seedMembership(accountID, older, "auditor", models.StatusActive, "2026-06-01 00:00:00", uuid.Nil, "")
	s.seedMembership(otherID, outsider, "owner", models.StatusActive, "2026-01-01 00:00:00", uuid.Nil, "")
	s.seedMembership(accountID, removed, "user", models.StatusActive, "2026-03-01 00:00:00", uuid.Nil, "2026-04-01 00:00:00")

	session := s.signIn(admin.Email)
	page := s.listAccountUsers(session.AccessToken, accountID, "")
	page.AssertOk()
	body := s.listedAccountUsers(page)
	s.Equal(int64(4), body.Total)
	s.Equal(20, body.Limit)
	s.Equal(0, body.Offset)
	s.Require().Len(body.Data, 4)
	s.Equal([]string{newer.String(), high.String(), low.String(), older.String()}, accountUserIDs(body.Data))

	s.Equal("owner", body.Data[0].Role)
	s.Equal(models.StatusActive, body.Data[0].Status)
	s.Equal(accountID.String(), body.Data[0].AccountID)
	s.Equal(newer.String(), body.Data[0].UserID)
	s.Equal("Newer Ned", body.Data[0].User.FullName)
	s.Nil(body.Data[0].User.SuspendedAt)
	s.False(body.Data[0].User.TotpEnabled)
	s.Empty(body.Data[0].AddedBy)

	s.Equal("admin", body.Data[1].Role)
	s.Equal(models.MembershipStatusSuspended, body.Data[1].Status)
	s.Equal(admin.ID.String(), body.Data[1].AddedBy)
	s.Equal("High Hannah", body.Data[1].User.FullName)
	s.Equal("active", body.Data[1].User.Status)
	s.Require().NotNil(body.Data[1].User.SuspendedAt)
	s.Equal("2026-10-01T12:00:00Z", *body.Data[1].User.SuspendedAt)
	s.True(body.Data[1].User.TotpEnabled)

	s.Equal("Low Leo", body.Data[2].User.FullName)
	s.Equal("Older Olive", body.Data[3].User.FullName)
	s.Equal("auditor", body.Data[3].Role)

	raw, err := page.Content()
	s.Require().NoError(err)
	s.assertAccountUsersShape(raw)
	for _, row := range body.Data {
		s.NotEqual(admin.ID.String(), row.User.ID)
	}
	s.NotContains(raw, outsider.String())
	s.NotContains(raw, removed.String())

	second := s.listAccountUsers(session.AccessToken, accountID, "?limit=1&offset=1")
	second.AssertOk()
	skipped := s.listedAccountUsers(second)
	s.Equal(1, skipped.Limit)
	s.Equal(1, skipped.Offset)
	s.Equal(int64(4), skipped.Total)
	s.Require().Len(skipped.Data, 1)
	s.Equal(high.String(), skipped.Data[0].User.ID)

	past := s.listAccountUsers(session.AccessToken, accountID, "?limit=20&offset=4")
	past.AssertOk()
	emptyPage := s.listedAccountUsers(past)
	s.Equal(int64(4), emptyPage.Total)
	s.Empty(emptyPage.Data)
	pastRaw, err := past.Content()
	s.Require().NoError(err)
	s.Contains(pastRaw, `"data":[]`)

	none := s.listAccountUsers(session.AccessToken, emptyID, "")
	none.AssertOk()
	noneBody := s.listedAccountUsers(none)
	s.Equal(int64(0), noneBody.Total)
	s.Empty(noneBody.Data)

	missing := s.listAccountUsers(session.AccessToken, uuid.New(), "")
	missing.AssertNotFound()
	s.Equal(responses.CodeNotFound, s.accountUsersError(missing).Code)
	s.Equal("account not found", s.accountUsersError(missing).Message)

	badID := s.listAccountUsersPath(session.AccessToken, "/v1/platform/accounts/not-a-uuid/users")
	badID.AssertStatus(400)
	s.Equal(responses.CodeInvalidRequest, s.accountUsersError(badID).Code)
}

func (s *PlatformAccountUsersTestSuite) TestA_Member_CannotListAccountUsers() {
	member := s.seedUser(false)
	accountID := s.seedAccountUsersAccount("hidden")
	person := s.seedAccountPerson("2024-01-01 00:00:00", "Hidden Hana", false, false, "")
	s.seedMembership(accountID, person, "owner", models.StatusActive, "2024-01-01 00:00:00", uuid.Nil, "")
	session := s.signIn(member.Email)

	resp := s.listAccountUsers(session.AccessToken, accountID, "")
	resp.AssertForbidden()
	s.Equal(responses.CodeForbidden, s.accountUsersError(resp).Code)
	s.Equal("you do not have permission to view account users", s.accountUsersError(resp).Message)
	raw, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(raw, person.String())
	s.NotContains(raw, "Hidden Hana")

	anonymous, err := s.Http(s.T()).Get("/v1/platform/accounts/" + accountID.String() + "/users")
	s.Require().NoError(err)
	anonymous.AssertUnauthorized()
}

func (s *PlatformAccountUsersTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountUsersTestSuite) seedAccountUsersAccount(name string) uuid.UUID {
	s.T().Helper()
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO accounts (id, name, status, view_all_wallets, environment, created_at, updated_at)
		 VALUES (?, ?, ?, TRUE, 'prod', NOW(), NOW())`,
		id, name+"-"+id.String()[:8], models.StatusActive,
	)
	s.Require().NoError(err)
	return id
}

func (s *PlatformAccountUsersTestSuite) seedAccountPerson(createdAt, fullName string, suspended, totp bool, markerPrefix string) uuid.UUID {
	return s.seedAccountPersonAt(uuid.New(), createdAt, fullName, suspended, totp, markerPrefix)
}

func (s *PlatformAccountUsersTestSuite) seedAccountPersonAt(id uuid.UUID, createdAt, fullName string, suspended, totp bool, markerPrefix string) uuid.UUID {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, full_name, status, totp_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
		id, "person-"+id.String()[:8]+"@example.com", markerPrefix+passwordHashMarker, fullName, totp, createdAt, createdAt,
	)
	s.Require().NoError(err)
	if suspended || totp {
		suspendedAt := any(nil)
		if suspended {
			suspendedAt = "2026-10-01 12:00:00+00"
		}
		_, err = facades.Orm().Query().Exec(
			`UPDATE users SET suspended_at = ?, suspension_reason = ?, sessions_revoked_at = '2026-10-02 12:00:00+00' WHERE id = ?`,
			suspendedAt, markerPrefix+suspensionReasonMarker, id,
		)
		s.Require().NoError(err)
	}
	if totp {
		_, err = facades.Orm().Query().Exec(`
			INSERT INTO mfa_credentials (
				id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
			) VALUES (?, 'users', ?, ?, 0, NOW(), NOW())`,
			uuid.New(), id, markerPrefix+totpSecretMarker,
		)
		s.Require().NoError(err)
		_, err = facades.Orm().Query().Exec(`
			INSERT INTO mfa_backup_codes (
				id, subject_type, subject_id, code_hash, created_at, updated_at
			) VALUES (?, 'users', ?, ?, NOW(), NOW())`,
			uuid.New(), id, markerPrefix+recoveryCodeHashMarker,
		)
		s.Require().NoError(err)
		_, err = facades.Orm().Query().Exec(
			`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, updated_at)
			 VALUES (?, ?, ?, NOW() + INTERVAL '1 day', NOW(), NOW())`,
			uuid.New(), id, markerPrefix+refreshTokenHashMarker,
		)
		s.Require().NoError(err)
	}
	return id
}

func (s *PlatformAccountUsersTestSuite) seedMembership(accountID, userID uuid.UUID, role, status, createdAt string, addedBy uuid.UUID, deletedAt string) {
	s.T().Helper()
	var added any
	if addedBy != uuid.Nil {
		added = addedBy
	}
	var deleted any
	if deletedAt != "" {
		deleted = deletedAt
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (
			id, account_id, user_id, role, status, added_by, deleted_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New(), accountID, userID, role, status, added, deleted, createdAt, createdAt,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountUsersTestSuite) listAccountUsers(bearer string, accountID uuid.UUID, query string) contractstesting.Response {
	return s.listAccountUsersPath(bearer, "/v1/platform/accounts/"+accountID.String()+"/users"+query)
}

func (s *PlatformAccountUsersTestSuite) listAccountUsersPath(bearer, path string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		Get(path)
	s.Require().NoError(err)
	return resp
}

type listedAccountUsers struct {
	Data   []listedAccountUser `json:"data"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type listedAccountUser struct {
	ID        string                   `json:"id"`
	AccountID string                   `json:"account_id"`
	UserID    string                   `json:"user_id"`
	Role      string                   `json:"role"`
	Status    string                   `json:"status"`
	AddedBy   string                   `json:"added_by"`
	User      listedAccountUserProfile `json:"user"`
}

type listedAccountUserProfile struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	FullName    string  `json:"full_name"`
	Status      string  `json:"status"`
	SuspendedAt *string `json:"suspended_at"`
	TotpEnabled bool    `json:"totp_enabled"`
}

func (s *PlatformAccountUsersTestSuite) listedAccountUsers(resp contractstesting.Response) listedAccountUsers {
	s.T().Helper()
	var body listedAccountUsers
	s.decode(resp, &body)
	return body
}

func accountUserIDs(rows []listedAccountUser) []string {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].User.ID
	}
	return ids
}

func (s *PlatformAccountUsersTestSuite) accountUsersError(resp contractstesting.Response) struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	s.T().Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	return body.Error
}

func (s *PlatformAccountUsersTestSuite) assertAccountUsersShape(raw string) {
	s.T().Helper()
	for _, marker := range []string{
		passwordHashMarker,
		totpSecretMarker,
		recoveryCodeHashMarker,
		refreshTokenHashMarker,
		suspensionReasonMarker,
		"removed-",
	} {
		s.NotContains(raw, marker)
	}
	var payload any
	s.Require().NoError(json.Unmarshal([]byte(raw), &payload))
	s.assertAccountUserKeys(payload, "data", "limit", "offset", "total")
	object, ok := payload.(map[string]any)
	s.Require().True(ok)
	s.walkAccountUserSecrets(object["data"])
	rows, ok := object["data"].([]any)
	s.Require().True(ok)
	s.Require().Len(rows, 4)
	s.assertAccountUserKeys(rows[0], "account_id", "created_at", "id", "role", "status", "updated_at", "user", "user_id")
	s.assertAccountUserKeys(rows[1], "account_id", "added_by", "created_at", "id", "role", "status", "updated_at", "user", "user_id")
	for _, row := range rows {
		record, ok := row.(map[string]any)
		s.Require().True(ok)
		s.assertAccountUserKeys(record["user"], "email", "full_name", "id", "status", "suspended_at", "totp_enabled")
		_, deleted := record["deleted_at"]
		s.False(deleted)
	}
	s.Contains(raw, `"totp_enabled":true`)
	s.Contains(raw, `"totp_enabled":false`)
	s.NotContains(strings.ToLower(raw), "password_hash")
	s.NotContains(strings.ToLower(raw), "totp_secret")
	s.NotContains(raw, "preferences")
	s.NotContains(raw, "default_account_id")
}

func (s *PlatformAccountUsersTestSuite) assertAccountUserKeys(node any, keys ...string) {
	s.T().Helper()
	object, ok := node.(map[string]any)
	s.Require().True(ok)
	s.Require().Len(object, len(keys))
	for _, key := range keys {
		_, present := object[key]
		s.True(present, key)
	}
}

func (s *PlatformAccountUsersTestSuite) walkAccountUserSecrets(node any) {
	s.T().Helper()
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			lower := strings.ToLower(key)
			for _, forbidden := range []string{
				"password", "password_hash", "totp_secret", "secret", "recovery",
				"code_hash", "refresh_token", "access_token", "token_hash",
				"sessions_revoked_at", "suspension_reason", "preferences", "default_account_id",
			} {
				s.NotEqual(forbidden, lower)
			}
			s.walkAccountUserSecrets(child)
		}
	case []any:
		for _, child := range value {
			s.walkAccountUserSecrets(child)
		}
	}
}
