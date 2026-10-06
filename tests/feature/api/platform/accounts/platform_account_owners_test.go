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
	"github.com/macrowallets/waas/tests/testutil"
)

// PlatformAccountOwnersTestSuite is POST /v1/platform/accounts/{accountId}/owners.
// S3.4.1 names attach owner — recovery and accounts.owners. A new or restored
// membership is 201. An existing active owner is 204 and does not write.
type PlatformAccountOwnersTestSuite struct {
	authSuite
}

func TestPlatformAccountOwnersSuite(t *testing.T) {
	suite.Run(t, new(PlatformAccountOwnersTestSuite))
}

func (s *PlatformAccountOwnersTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountOwnersTestSuite) TestAPlatformAdminAttachesAnOwner() {
	admin := s.seedUser(false)
	s.grantOwnerAdmin(admin.ID)
	session := s.signIn(admin.Email)

	accountID := s.seedOwnerAccount("attach", models.StatusActive)
	person := s.seedOwnerPerson(false)
	s.seedOwnerToken(accountID, person.ID)

	beforeActivity := s.ownerCount(`SELECT count(*) FROM account_activity WHERE account_id = ?`, accountID)
	beforeTokens := s.ownerCount(`SELECT count(*) FROM access_tokens WHERE account_id = ?`, accountID)
	beforeRefresh := s.ownerCount(`SELECT count(*) FROM refresh_tokens WHERE user_id = ?`, person.ID)
	beforeInvites := s.ownerCount(`SELECT count(*) FROM account_invites WHERE account_id = ?`, accountID)

	created := s.postOwner(session.AccessToken, accountID, person.Email)
	created.AssertStatus(201)
	body := s.ownerBody(created)
	s.Equal(models.AccountRoleOwner, body.Role)
	s.Equal(models.MembershipStatusActive, body.Status)
	s.Equal(accountID.String(), body.AccountID)
	s.Equal(person.ID.String(), body.UserID)
	s.Equal(admin.ID.String(), body.AddedBy)
	s.Equal(person.Email, body.User.Email)
	s.Equal("Ada Owner", body.User.FullName)
	s.True(body.User.TotpEnabled)
	s.Nil(body.User.SuspendedAt)
	raw, err := created.Content()
	s.Require().NoError(err)
	s.assertOwnerSecrets(raw)
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL`,
		accountID, person.ID,
	))
	s.Equal(models.AccountRoleOwner, s.ownerText(
		`SELECT role FROM account_users WHERE id = ?`, body.ID,
	))
	s.Equal(int64(0), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ?`,
		accountID, admin.ID,
	))
	s.Equal(int64(1), s.ownerCount(`SELECT count(*) FROM users WHERE email = ?`, person.Email))
	s.Equal(beforeInvites, s.ownerCount(`SELECT count(*) FROM account_invites WHERE account_id = ?`, accountID))
	s.Equal(beforeTokens, s.ownerCount(`SELECT count(*) FROM access_tokens WHERE account_id = ?`, accountID))
	s.Equal(beforeRefresh, s.ownerCount(`SELECT count(*) FROM refresh_tokens WHERE user_id = ?`, person.ID))
	s.Equal(beforeActivity, s.ownerCount(`SELECT count(*) FROM account_activity WHERE account_id = ?`, accountID))
	s.Nil(s.ownerNullable(`SELECT sessions_revoked_at::text FROM users WHERE id = ?`, person.ID))
	s.Nil(s.ownerNullable(`SELECT revoked_at::text FROM refresh_tokens WHERE user_id = ?`, person.ID))
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM activity_log WHERE subject_type = 'account_user' AND subject_id = ? AND event = 'created'`,
		body.ID,
	))
	props := s.ownerText(
		`SELECT properties::text FROM activity_log WHERE subject_type = 'account_user' AND subject_id = ?`,
		body.ID,
	)
	s.Contains(props, accountID.String())
	s.Contains(props, person.ID.String())
	s.Contains(props, models.AccountRoleOwner)
	s.NotContains(props, passwordHashMarker)
	s.NotContains(props, totpSecretMarker)
	s.NotContains(props, "member.attached")
	storedUpdatedAt := s.ownerText(`SELECT updated_at::text FROM account_users WHERE id = ?`, body.ID)

	again := s.postOwner(session.AccessToken, accountID, person.Email)
	again.AssertNoContent()
	againRaw, err := again.Content()
	s.Require().NoError(err)
	s.Empty(againRaw)
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL`,
		accountID, person.ID,
	))
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM activity_log WHERE subject_type = 'account_user' AND subject_id = ?`,
		body.ID,
	))
	s.Equal(storedUpdatedAt, s.ownerText(`SELECT updated_at::text FROM account_users WHERE id = ?`, body.ID))

	demoted := s.seedOwnerPerson(false)
	demotedAccount := s.seedOwnerAccount("demote", models.StatusActive)
	s.seedOwnerMembership(demotedAccount, demoted.ID, models.AccountRoleAdmin, models.MembershipStatusActive, "")
	promoted := s.postOwner(session.AccessToken, demotedAccount, demoted.Email)
	promoted.AssertStatus(201)
	promotedBody := s.ownerBody(promoted)
	s.Equal(models.AccountRoleOwner, promotedBody.Role)
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ?`,
		demotedAccount, demoted.ID,
	))
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM activity_log WHERE subject_type = 'account_user' AND subject_id = ? AND event = 'updated'`,
		promotedBody.ID,
	))

	removed := s.seedOwnerPerson(false)
	removedAccount := s.seedOwnerAccount("restore", models.StatusActive)
	s.seedOwnerMembership(removedAccount, removed.ID, models.AccountRoleUser, models.MembershipStatusSuspended, "2026-01-01 00:00:00")
	restored := s.postOwner(session.AccessToken, removedAccount, removed.Email)
	restored.AssertStatus(201)
	restoredBody := s.ownerBody(restored)
	s.Equal(models.AccountRoleOwner, restoredBody.Role)
	s.Equal(models.MembershipStatusActive, restoredBody.Status)
	s.Equal(int64(1), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ? AND deleted_at IS NULL AND role = 'owner' AND status = 'active'`,
		removedAccount, removed.ID,
	))
}

func (s *PlatformAccountOwnersTestSuite) TestAMemberCannotAttachAnOwner() {
	member := s.seedUser(false)
	accountID := s.seedOwnerAccount("hidden", models.StatusActive)
	person := s.seedOwnerPerson(false)
	s.seedOwnerMembership(accountID, member.ID, models.AccountRoleOwner, models.MembershipStatusActive, "")
	session := s.signIn(member.Email)

	resp := s.postOwner(session.AccessToken, accountID, person.Email)
	resp.AssertForbidden()
	s.Equal(responses.CodeForbidden, s.ownerError(resp).Code)
	s.Equal("you do not have permission to attach an account owner", s.ownerError(resp).Message)
	raw, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(raw, person.Email)
	s.NotContains(raw, accountID.String())
	s.Equal(int64(0), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE account_id = ? AND user_id = ?`,
		accountID, person.ID,
	))
	s.Equal(models.StatusActive, s.ownerText(`SELECT status FROM accounts WHERE id = ?`, accountID))
	s.Equal(int64(0), s.ownerCount(
		`SELECT count(*) FROM activity_log WHERE subject_type = 'account_user' AND properties::text LIKE ?`,
		"%"+person.ID.String()+"%",
	))

	anonymous, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/platform/accounts/"+accountID.String()+"/owners", strings.NewReader(`{"email":"`+person.Email+`"}`))
	s.Require().NoError(err)
	anonymous.AssertUnauthorized()
}

func (s *PlatformAccountOwnersTestSuite) TestUnknownAccountAndUnknownUser() {
	admin := s.seedUser(false)
	s.grantOwnerAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnerAccount("known", models.StatusActive)
	person := s.seedOwnerPerson(false)

	missingAccount := s.postOwner(session.AccessToken, uuid.New(), person.Email)
	missingAccount.AssertNotFound()
	s.Equal(responses.CodeNotFound, s.ownerError(missingAccount).Code)
	s.Equal("account not found", s.ownerError(missingAccount).Message)
	s.Equal(int64(0), s.ownerCount(
		`SELECT count(*) FROM account_users WHERE user_id = ?`, person.ID,
	))

	missingUser := s.postOwner(session.AccessToken, accountID, "missing-owner@example.com")
	missingUser.AssertNotFound()
	s.Equal(responses.CodeNotFound, s.ownerError(missingUser).Code)
	s.Equal("user not found", s.ownerError(missingUser).Message)
	s.Equal(int64(0), s.ownerCount(`SELECT count(*) FROM users WHERE email = ?`, "missing-owner@example.com"))
	s.Equal(int64(0), s.ownerCount(`SELECT count(*) FROM account_users WHERE account_id = ?`, accountID))

	invalid := s.postOwner(session.AccessToken, accountID, "not-an-email")
	invalid.AssertStatus(422)
	s.Equal(responses.CodeValidationFailed, s.ownerError(invalid).Code)

	onlyID := s.postOwnerRaw(session.AccessToken, accountID, `{"user_id":"`+person.ID.String()+`"}`)
	onlyID.AssertStatus(422)
	s.Equal(responses.CodeValidationFailed, s.ownerError(onlyID).Code)
	s.Equal(int64(0), s.ownerCount(`SELECT count(*) FROM account_users WHERE account_id = ?`, accountID))

	badID := s.postOwnerPath(session.AccessToken, "/v1/platform/accounts/not-a-uuid/owners", `{"email":"`+person.Email+`"}`)
	badID.AssertStatus(400)
	s.Equal(responses.CodeInvalidRequest, s.ownerError(badID).Code)
}

func (s *PlatformAccountOwnersTestSuite) TestAFrozenAccountStillAcceptsTheAttach() {
	admin := s.seedUser(false)
	s.grantOwnerAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnerAccount("frozen", models.AccountStatusFrozen)
	person := s.seedOwnerPerson(false)

	resp := s.postOwner(session.AccessToken, accountID, person.Email)
	resp.AssertStatus(201)
	body := s.ownerBody(resp)
	s.Equal(models.AccountRoleOwner, body.Role)
	s.Equal(models.MembershipStatusActive, body.Status)
	s.Equal(models.AccountStatusFrozen, s.ownerText(`SELECT status FROM accounts WHERE id = ?`, accountID))
	raw, err := resp.Content()
	s.Require().NoError(err)
	s.assertOwnerSecrets(raw)
}

type ownerMembershipBody struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	AddedBy   string `json:"added_by"`
	User      struct {
		ID          string  `json:"id"`
		Email       string  `json:"email"`
		FullName    string  `json:"full_name"`
		Status      string  `json:"status"`
		SuspendedAt *string `json:"suspended_at"`
		TotpEnabled bool    `json:"totp_enabled"`
	} `json:"user"`
}

type ownerPerson struct {
	ID    uuid.UUID
	Email string
}

func (s *PlatformAccountOwnersTestSuite) grantOwnerAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountOwnersTestSuite) seedOwnerAccount(name, status string) uuid.UUID {
	s.T().Helper()
	id := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO accounts (id, name, status, view_all_wallets, environment, created_at, updated_at)
		 VALUES (?, ?, ?, TRUE, 'prod', NOW(), NOW())`,
		id, name+"-"+id.String()[:8], status,
	)
	s.Require().NoError(err)
	return id
}

func (s *PlatformAccountOwnersTestSuite) seedOwnerPerson(plain bool) ownerPerson {
	s.T().Helper()
	id := uuid.New()
	email := "owner-" + id.String()[:8] + "@example.com"
	fullName := "Ada Owner"
	totp := true
	if plain {
		fullName = "Plain Pat"
		totp = false
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, full_name, status, totp_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', ?, NOW(), NOW())`,
		id, email, passwordHashMarker, fullName, totp,
	)
	s.Require().NoError(err)
	if !plain {
		_, err = facades.Orm().Query().Exec(
			`UPDATE users SET suspended_at = NULL, suspension_reason = ? WHERE id = ?`,
			suspensionReasonMarker, id,
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
	}
	return ownerPerson{ID: id, Email: email}
}

func (s *PlatformAccountOwnersTestSuite) seedOwnerMembership(accountID, userID uuid.UUID, role, status, deletedAt string) {
	s.T().Helper()
	var deleted any
	if deletedAt != "" {
		deleted = deletedAt
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (id, account_id, user_id, role, status, deleted_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		uuid.New(), accountID, userID, role, status, deleted,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountOwnersTestSuite) seedOwnerToken(accountID, createdBy uuid.UUID) {
	s.T().Helper()
	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, created_by, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, createdBy, "kept", "hash-"+tokenID.String()[:8], "{}",
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, NOW() + INTERVAL '1 day', NOW(), NOW())`,
		uuid.New(), createdBy, refreshTokenHashMarker,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountOwnersTestSuite) postOwner(bearer string, accountID uuid.UUID, email string) contractstesting.Response {
	return s.postOwnerPath(bearer, "/v1/platform/accounts/"+accountID.String()+"/owners", `{"email":"`+email+`"}`)
}

func (s *PlatformAccountOwnersTestSuite) postOwnerRaw(bearer string, accountID uuid.UUID, body string) contractstesting.Response {
	return s.postOwnerPath(bearer, "/v1/platform/accounts/"+accountID.String()+"/owners", body)
}

func (s *PlatformAccountOwnersTestSuite) postOwnerPath(bearer, path, body string) contractstesting.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		WithHeader("Content-Type", "application/json").
		Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PlatformAccountOwnersTestSuite) ownerBody(resp contractstesting.Response) ownerMembershipBody {
	s.T().Helper()
	var body ownerMembershipBody
	s.decode(resp, &body)
	s.NotEmpty(body.ID)
	return body
}

func (s *PlatformAccountOwnersTestSuite) ownerError(resp contractstesting.Response) struct {
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

func (s *PlatformAccountOwnersTestSuite) ownerCount(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformAccountOwnersTestSuite) ownerText(query string, args ...any) string {
	s.T().Helper()
	var text string
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&text))
	return text
}

func (s *PlatformAccountOwnersTestSuite) ownerNullable(query string, args ...any) *string {
	s.T().Helper()
	var text *string
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&text))
	return text
}

func (s *PlatformAccountOwnersTestSuite) assertOwnerSecrets(raw string) {
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
	s.NotContains(strings.ToLower(raw), "password_hash")
	s.NotContains(strings.ToLower(raw), "totp_secret")
	s.NotContains(raw, "preferences")
	s.NotContains(raw, "sessions_revoked_at")
	var payload any
	s.Require().NoError(json.Unmarshal([]byte(raw), &payload))
	s.walkOwnerSecrets(payload)
}

func (s *PlatformAccountOwnersTestSuite) walkOwnerSecrets(node any) {
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
			s.walkOwnerSecrets(child)
		}
	case []any:
		for _, child := range value {
			s.walkOwnerSecrets(child)
		}
	}
}
