package users

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type MfaSealTestSuite struct {
	authSuite
}

func TestMfa_Seal_Suite(t *testing.T) {
	suite.Run(t, new(MfaSealTestSuite))
}

func (s *MfaSealTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *MfaSealTestSuite) TestEnrolled_Secret_IsSealedAndVerifyStillWorks() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)

	setup := s.authedPost(session.AccessToken, "/v1/users/me/totp/setup", "")
	setup.AssertOk()
	var setupBody struct {
		Secret string `json:"secret"`
	}
	s.decode(setup, &setupBody)
	s.Require().NotEmpty(setupBody.Secret)

	stored := s.text(`SELECT secret FROM mfa_credentials WHERE subject_type = 'users' AND subject_id = ?`, user.ID)
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(stored, setupBody.Secret) {
		s.Fail("enrolled totp secret is not sealed in mfa_credentials")
	}
	setupContent, err := setup.Content()
	s.Require().NoError(err)
	if strings.Contains(setupContent, stored) || strings.Contains(setupContent, "enc:v1:") {
		s.Fail("setup response contains the sealed totp secret")
	}

	confirm := s.authedPost(session.AccessToken, "/v1/users/me/totp/verify", `{"code":"`+s.currentCode(setupBody.Secret)+`"}`)
	confirm.AssertOk()
	s.refuseSecret(confirm, setupBody.Secret, stored)
	s.Equal(int64(10), s.rows(`
		SELECT count(*) FROM mfa_backup_codes WHERE subject_type = 'users' AND subject_id = ?`, user.ID))

	me := s.getMe(session.AccessToken)
	me.AssertOk()
	s.refuseSecret(me, setupBody.Secret, stored)

	_, challenge := s.loginAs(user.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	replayed, _ := s.verifyTwoFactor(challenge.ChallengeToken, s.currentCode(setupBody.Secret), "")
	replayed.AssertUnauthorized()
	s.refuseSecret(replayed, setupBody.Secret, stored)

	var confirmed struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	s.decode(confirm, &confirmed)
	s.Require().NotEmpty(confirmed.RecoveryCodes)
	verified, body := s.verifyTwoFactor(challenge.ChallengeToken, "", confirmed.RecoveryCodes[0])
	verified.AssertOk()
	s.NotEmpty(body.AccessToken)
	s.refuseSecret(verified, setupBody.Secret, stored)
	s.Zero(s.rows(`SELECT count(*) FROM activity_log WHERE properties::text LIKE '%enc:v1:%'`))
	s.Zero(s.rows(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%enc:v1:%'`))
}

func (s *MfaSealTestSuite) TestUnsealed_Secret_FailsClosed() {
	user := s.seedUser(false)
	_, err := facades.Orm().Query().Exec(`UPDATE users SET totp_enabled = TRUE WHERE id = ?`, user.ID)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, 'clear-text-marker', 0, NOW(), NOW())`,
		uuid.New(), user.ID)
	s.Require().NoError(err)

	_, challenge := s.loginAs(user.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	resp, _ := s.verifyTwoFactor(challenge.ChallengeToken, "000000", "")
	resp.AssertStatus(500)
	s.refuseSecret(resp, "clear-text-marker", "clear-text-marker")
}

func (s *MfaSealTestSuite) text(query string, args ...any) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&value))
	return value
}

func (s *MfaSealTestSuite) rows(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *MfaSealTestSuite) refuseSecret(resp contractstesting.Response, plaintext, stored string) {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	if strings.Contains(content, "enc:v1:") || (stored != "" && strings.Contains(content, stored)) || (plaintext != "" && strings.Contains(content, plaintext)) {
		s.Fail("http response contains the totp secret")
	}
}
