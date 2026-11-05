package users

import (
	"testing"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/pquerna/otp/totp"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// StepUpRoutesTestSuite checks RequireStepUp on the sensitive writes: the
// account routes (tokens, settings) and the platform ones (features, mail test
// and section cache as the exceptions).
type StepUpRoutesTestSuite struct {
	authSuite
}

func TestStepUp_Routes_Suite(t *testing.T) {
	support.RunSuite(t, new(StepUpRoutesTestSuite))
}

func (s *StepUpRoutesTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

const (
	stepUpSettingsBody = `{"require_2fa":false}`
	stepUpFeatureBody  = `{"enabled":true}`
)

func (s *StepUpRoutesTestSuite) enableFlag() {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO global_features ("key", enabled, created_at, updated_at) VALUES (?, TRUE, NOW(), NOW())`,
		features.FlagSettings2FARequired,
	)
	s.Require().NoError(err)
}

func (s *StepUpRoutesTestSuite) account(userID uuid.UUID, role string) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "StepUp " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role,
	}))
	return accountID
}

func (s *StepUpRoutesTestSuite) platformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`, userID,
	)
	s.Require().NoError(err)
}

func (s *StepUpRoutesTestSuite) session(email string, user seededAuthUser, recovery int) loginBody {
	s.T().Helper()
	_, challenge := s.loginAs(email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	resp, session := s.verifyTwoFactor(challenge.ChallengeToken, "", user.RecoveryCodes[recovery])
	resp.AssertOk()
	return session
}

func (s *StepUpRoutesTestSuite) grant(user seededAuthUser, bearer string) {
	s.T().Helper()
	code, err := totp.GenerateCode(user.TOTPSecret, time.Now().Add(30*time.Second))
	s.Require().NoError(err)
	s.authedPost(bearer, "/v1/users/me/step-up", `{"totp_code":"`+code+`"}`).AssertOk()
}

func (s *StepUpRoutesTestSuite) patchSettings(bearer string, accountID uuid.UUID) contractstesting.Response {
	return s.Patch("/v1/accounts/"+accountID.String()+"/settings/account_security",
		support.Session{AccessToken: bearer}, stepUpSettingsBody)
}

func (s *StepUpRoutesTestSuite) mintToken(bearer string, accountID uuid.UUID) contractstesting.Response {
	return s.authedPost(bearer, "/v1/accounts/"+accountID.String()+"/tokens", `{"name":"t"}`)
}

func (s *StepUpRoutesTestSuite) patchFeature(bearer string) contractstesting.Response {
	return s.Patch("/v1/platform/features/"+features.FlagWithdrawalsEnabled,
		support.Session{AccessToken: bearer}, stepUpFeatureBody)
}

func (s *StepUpRoutesTestSuite) assertStepUpRequired(resp contractstesting.Response) {
	s.T().Helper()
	s.AssertError(resp, 403, "step_up_required", "step_up_required")
}

func (s *StepUpRoutesTestSuite) assertNotStepUp(resp contractstesting.Response) {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, "step_up_required")
}

func (s *StepUpRoutesTestSuite) TestFlagOff_WritesPassWithoutAWindow() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	session := s.session(owner.Email, owner, 0)

	s.patchSettings(session.AccessToken, accountID).AssertOk()
	s.patchFeature(session.AccessToken).AssertOk()
}

func (s *StepUpRoutesTestSuite) TestFlagOn_WithoutTwoFactorIsAskedToEnroll() {
	owner := s.seedUser(false)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	session := s.signIn(owner.Email)
	s.enableFlag()

	enroll := func(resp contractstesting.Response) {
		s.AssertError(resp, 403, "two_factor_enrollment_required", "two_factor_enrollment_required")
	}
	enroll(s.patchSettings(session.AccessToken, accountID))
	enroll(s.mintToken(session.AccessToken, accountID))
	enroll(s.patchFeature(session.AccessToken))
}

func (s *StepUpRoutesTestSuite) TestFlagOn_WithoutAWindowIs403AndAWrongCodeDoesNotOpenOne() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	session := s.session(owner.Email, owner, 0)
	s.enableFlag()

	s.assertStepUpRequired(s.patchSettings(session.AccessToken, accountID))
	s.assertStepUpRequired(s.mintToken(session.AccessToken, accountID))
	s.assertStepUpRequired(s.patchFeature(session.AccessToken))

	wrong := s.authedPost(session.AccessToken, "/v1/users/me/step-up", `{"totp_code":"000000"}`)
	s.AssertError(wrong, 401, "unauthorized", "invalid 2FA code")
	s.assertStepUpRequired(s.patchSettings(session.AccessToken, accountID))
}

func (s *StepUpRoutesTestSuite) TestFlagOn_ARightCodeOpensTheWritesOfAccountAndPlatform() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	session := s.session(owner.Email, owner, 0)
	s.enableFlag()

	s.grant(owner, session.AccessToken)

	s.patchSettings(session.AccessToken, accountID).AssertOk()
	s.assertNotStepUp(s.mintToken(session.AccessToken, accountID))
	s.patchFeature(session.AccessToken).AssertOk()
}

func (s *StepUpRoutesTestSuite) TestFlagOn_TheWindowOfAnotherSessionDoesNotCount() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	granted := s.session(owner.Email, owner, 0)
	other := s.session(owner.Email, owner, 1)
	s.enableFlag()

	s.grant(owner, granted.AccessToken)

	s.patchSettings(granted.AccessToken, accountID).AssertOk()
	s.assertStepUpRequired(s.patchSettings(other.AccessToken, accountID))
	s.assertStepUpRequired(s.patchFeature(other.AccessToken))
}

func (s *StepUpRoutesTestSuite) TestFlagOn_LogoutEndsTheWindow() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	session := s.session(owner.Email, owner, 0)
	s.enableFlag()
	s.grant(owner, session.AccessToken)
	s.patchSettings(session.AccessToken, accountID).AssertOk()

	s.authedPost(session.AccessToken, "/v1/auth/logout", "").AssertNoContent()
	s.assertSessionRevoked(s.patchSettings(session.AccessToken, accountID))

	// A new session of the same user starts without the window.
	// The login's recovery code is spent; the second one signs in.
	fresh := s.session(owner.Email, owner, 1)
	s.assertStepUpRequired(s.patchSettings(fresh.AccessToken, accountID))
}

func (s *StepUpRoutesTestSuite) TestFlagOn_AnExpiredWindowIs403Again() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	s.platformAdmin(owner.ID)
	session := s.session(owner.Email, owner, 0)
	s.enableFlag()
	s.grant(owner, session.AccessToken)
	s.patchSettings(session.AccessToken, accountID).AssertOk()

	sid, ok := authsvc.SessionIDOf(session.AccessToken)
	s.Require().True(ok)
	past := time.Now().Add(-time.Minute).Unix()
	s.Require().NoError(appfacades.Cache().Put("step-up:"+sid.String(), past, time.Minute))

	s.assertStepUpRequired(s.patchSettings(session.AccessToken, accountID))
	s.assertStepUpRequired(s.patchFeature(session.AccessToken))
}

func (s *StepUpRoutesTestSuite) TestFlagOn_ThePermissionIsCheckedBeforeTheWindow() {
	owner := s.seedUser(true)
	accountID := s.account(owner.ID, "owner")
	member := s.seedUser(true)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: member.ID, Role: "user",
	}))
	session := s.session(member.Email, member, 0)
	s.enableFlag()

	// Neither a settings write nor a token mint is allowed to the user role:
	// the answer is the permission's 403, never step_up_required.
	for _, resp := range []contractstesting.Response{
		s.patchSettings(session.AccessToken, accountID),
		s.mintToken(session.AccessToken, accountID),
	} {
		resp.AssertForbidden()
		s.assertNotStepUp(resp)
	}

	// A caller that is not a platform admin is refused by the platform gate first.
	resp := s.patchFeature(session.AccessToken)
	resp.AssertForbidden()
	s.assertNotStepUp(resp)
}

func (s *StepUpRoutesTestSuite) TestFlagOn_PlatformMailTestAndSectionCacheNeedNoWindow() {
	admin := s.seedUser(true)
	s.platformAdmin(admin.ID)
	session := s.session(admin.Email, admin, 0)
	s.enableFlag()

	s.assertNotStepUp(s.authedPost(session.AccessToken, "/v1/platform/settings/mail/test", `{}`))
	s.authedPost(session.AccessToken, "/v1/platform/settings/sections/mail/cache", `{}`).AssertNoContent()
	s.assertStepUpRequired(s.patchFeature(session.AccessToken))
}
