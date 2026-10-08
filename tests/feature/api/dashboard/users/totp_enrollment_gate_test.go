package users

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"unsafe"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const totpEnrollmentPassword = "correct-horse-battery"

// totpEnrollmentSuite checks S3.4.5: a member without confirmed TOTP receives
// 403 two_factor_enrollment_required on account routes when the
// user-2fa-required flag or account_security.require_2fa is on. A missing
// row stays off. Login and /v1/users/me/totp stay open. The suite never logs
// tokens or TOTP material.
type totpEnrollmentSuite struct {
	support.HTTPSuite
}

func TestTotpEnrollmentGate_TOTP_Enrollment(t *testing.T) {
	support.RunSuite(t, new(totpEnrollmentSuite))
}

func (s *totpEnrollmentSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *totpEnrollmentSuite) TestMissing_Flag_AndSettingLeaveTheAccountOpen() {
	accountID, token := s.member(false)
	s.Equal(http.StatusOK, s.statusOf(s.getAccount(token, accountID)))
	s.Equal(http.StatusOK, s.statusOf(s.getWallets(token, accountID)))
	s.Zero(s.flagRows(features.FlagUser2FARequired))
	s.Zero(s.settingRows("require_2fa"))
}

func (s *totpEnrollmentSuite) TestAccount_Flag_BlocksUntilTOTPIsConfirmed() {
	accountID, userID, token := s.memberID(false)
	s.Equal(http.StatusOK, s.statusOf(s.getAccount(token, accountID)))

	s.patchFlag(token, accountID, true)
	s.equalEnrollment(s.getAccount(token, accountID))
	s.equalEnrollment(s.getWallets(token, accountID))
	s.equalEnrollment(s.getChains(token, accountID))

	setup := s.post(token, "/v1/users/me/totp/setup", "")
	if status := s.statusOf(setup); status != http.StatusOK {
		s.Failf("totp setup", "status %d", status)
	}

	s.Require().NoError(s.setTotpEnabled(userID, true))
	s.Equal(http.StatusOK, s.statusOf(s.getAccount(token, accountID)))
	s.Equal(int64(1), s.flagRows(features.FlagUser2FARequired))
	s.Zero(s.flagRows(features.FlagWithdrawalsEnabled))
}

func (s *totpEnrollmentSuite) TestGlobal_Flag_BlocksLoginStillWorksAndConfirmedTOTPPasses() {
	accountID, userID, email := s.memberEmail(false)
	s.setGlobal(true)

	token := s.loginAccess(email)
	s.equalEnrollment(s.getAccount(token, accountID))

	s.Require().NoError(s.setTotpEnabled(userID, true))
	partial := s.loginPartial(email)
	s.AssertError(s.getAccount(partial, accountID), http.StatusUnauthorized, "unauthorized", "invalid token")

	verify := s.post("", "/v1/auth/2fa/verify", fmt.Sprintf(`{"challenge_token":%q,"code":"000000"}`, partial))
	s.AssertError(verify, http.StatusUnauthorized, "unauthorized", "invalid 2FA code")
	_, code := s.errorCode(verify)
	s.NotEqual(middleware.CodeTwoFactorEnrollmentRequired, code)
}

func (s *totpEnrollmentSuite) TestAccount_Policy_BlocksAndIdleOrWebhooksDoNot() {
	accountID, token := s.member(false)
	s.insertSetting(accountID, "account_security", "session_idle_minutes", "15")
	s.insertSetting(accountID, "account_webhooks", "default_events", "deposit.confirmed")
	s.Equal(http.StatusOK, s.statusOf(s.getAccount(token, accountID)))

	s.patchSettings(token, accountID, `{"require_2fa":true}`)
	s.equalEnrollment(s.getAccount(token, accountID))
	s.Zero(s.flagRows(features.FlagUser2FARequired))
}

func (s *totpEnrollmentSuite) member(totp bool) (uuid.UUID, string) {
	s.T().Helper()
	accountID, _, email := s.memberEmail(totp)
	return accountID, s.loginAccess(email)
}

func (s *totpEnrollmentSuite) memberID(totp bool) (uuid.UUID, uuid.UUID, string) {
	s.T().Helper()
	accountID, userID, email := s.memberEmail(totp)
	return accountID, userID, s.loginAccess(email)
}

func (s *totpEnrollmentSuite) memberEmail(totp bool) (uuid.UUID, uuid.UUID, string) {
	s.T().Helper()
	hash, err := authsvc.NewService().HashPassword(totpEnrollmentPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := "member-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, totp_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active", totp,
	)
	s.Require().NoError(err)
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "Totp " + accountID.String()[:8], Status: "active", Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner",
	}))
	return accountID, userID, email
}

func (s *totpEnrollmentSuite) loginAccess(email string) string {
	s.T().Helper()
	parsed := s.login(email)
	if parsed.AccessToken == "" || parsed.ChallengeToken != "" {
		s.FailNow("login did not return a session")
	}
	return parsed.AccessToken
}

func (s *totpEnrollmentSuite) loginPartial(email string) string {
	s.T().Helper()
	parsed := s.login(email)
	if !parsed.Requires2FA || parsed.ChallengeToken == "" || parsed.AccessToken != "" {
		s.FailNow("login did not return a 2FA challenge")
	}
	return parsed.ChallengeToken
}

func (s *totpEnrollmentSuite) login(email string) loginBody {
	s.T().Helper()
	response := s.post("", "/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, totpEnrollmentPassword))
	if status := s.statusOf(response); status != http.StatusOK {
		s.FailNowf("login", "status %d", status)
	}
	var parsed loginBody
	s.Require().NoError(json.Unmarshal([]byte(s.body(response)), &parsed))
	return parsed
}

func (s *totpEnrollmentSuite) getAccount(token string, accountID uuid.UUID) contractstestinghttp.Response {
	s.T().Helper()
	return s.get(token, "/v1/accounts/"+accountID.String(), "")
}

func (s *totpEnrollmentSuite) getWallets(token string, accountID uuid.UUID) contractstestinghttp.Response {
	s.T().Helper()
	return s.get(token, "/v1/wallets", accountID.String())
}

func (s *totpEnrollmentSuite) getChains(token string, accountID uuid.UUID) contractstestinghttp.Response {
	s.T().Helper()
	return s.get(token, "/v1/chains", accountID.String())
}

func (s *totpEnrollmentSuite) get(token, path, accountID string) contractstestinghttp.Response {
	s.T().Helper()
	return s.Get(path, support.Session{AccessToken: token, AccountID: accountID})
}

func (s *totpEnrollmentSuite) post(token, path, body string) contractstestinghttp.Response {
	s.T().Helper()
	return s.Post(path, support.Session{AccessToken: token}, body)
}

func (s *totpEnrollmentSuite) patchFlag(_ string, accountID uuid.UUID, enabled bool) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO features (account_id, "key", enabled, created_at, updated_at)
		 VALUES (?, ?, ?, NOW(), NOW())
		 ON CONFLICT (account_id, "key") DO UPDATE
		 SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		accountID, features.FlagUser2FARequired, enabled,
	)
	s.Require().NoError(err)
}

func (s *totpEnrollmentSuite) patchSettings(token string, accountID uuid.UUID, body string) {
	s.T().Helper()
	response := s.patch(token, "/v1/accounts/"+accountID.String()+"/settings/account_security", body)
	if status := s.statusOf(response); status != http.StatusOK {
		s.FailNowf("patch settings", "status %d", status)
	}
}

func (s *totpEnrollmentSuite) patch(token, path, body string) contractstestinghttp.Response {
	s.T().Helper()
	response := s.Patch(path, support.Session{AccessToken: token}, body)
	return response
}

func (s *totpEnrollmentSuite) equalEnrollment(response contractstestinghttp.Response) {
	s.T().Helper()
	s.AssertError(response, http.StatusForbidden, middleware.CodeTwoFactorEnrollmentRequired, middleware.CodeTwoFactorEnrollmentRequired)
}

func (s *totpEnrollmentSuite) errorCode(response contractstestinghttp.Response) (int, string) {
	s.T().Helper()
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(response)), &parsed))
	return s.statusOf(response), parsed.Error.Code
}

func (s *totpEnrollmentSuite) body(response contractstestinghttp.Response) string {
	s.T().Helper()
	content, err := response.Content()
	s.Require().NoError(err)
	return content
}

func (s *totpEnrollmentSuite) statusOf(response contractstestinghttp.Response) int {
	s.T().Helper()
	value := reflect.ValueOf(response)
	s.Require().Equal(reflect.Ptr, value.Kind())
	field := value.Elem().FieldByName("response")
	s.Require().True(field.IsValid())
	raw := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	httpResponse, ok := raw.Interface().(*http.Response)
	s.Require().True(ok)
	s.Require().NotNil(httpResponse)
	return httpResponse.StatusCode
}

// setTotpEnabled confirms or clears a user's TOTP. Confirming also stores the
// sealed secret in mfa_credentials: since f9901c4 that row, not users.totp_enabled
// alone, is what makes a second factor real, and the login challenge is refused
// for a user without it.
func (s *totpEnrollmentSuite) setTotpEnabled(userID uuid.UUID, enabled bool) error {
	if _, err := facades.Orm().Query().Exec(
		`UPDATE users SET totp_enabled = ? WHERE id = ?`,
		enabled, userID,
	); err != nil || !enabled {
		return err
	}
	secret, _, err := authsvc.NewService().GenerateTOTP(userID.String() + "@example.com")
	if err != nil {
		return err
	}
	sealed, err := facades.Crypt().EncryptString(secret)
	if err != nil {
		return err
	}
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, confirmed_at, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, ?, NOW(), 0, NOW(), NOW())
		ON CONFLICT (subject_type, subject_id) DO UPDATE
		SET secret = EXCLUDED.secret, confirmed_at = NOW(), updated_at = NOW()`,
		uuid.New(), userID, "enc:v1:"+sealed,
	)
	return err
}

func (s *totpEnrollmentSuite) setGlobal(enabled bool) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO global_features ("key", enabled, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`,
		features.FlagUser2FARequired, enabled,
	)
	s.Require().NoError(err)
}

func (s *totpEnrollmentSuite) insertSetting(accountID uuid.UUID, group, key, value string) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		accountID, group, key, value,
	)
	s.Require().NoError(err)
}

func (s *totpEnrollmentSuite) flagRows(key string) int64 {
	s.T().Helper()
	count, err := facades.Orm().Query().Model(&models.Feature{}).Where("key = ?", key).Count()
	s.Require().NoError(err)
	global, err := facades.Orm().Query().Model(&models.GlobalFeature{}).Where("key = ?", key).Count()
	s.Require().NoError(err)
	return count + global
}

func (s *totpEnrollmentSuite) settingRows(key string) int64 {
	s.T().Helper()
	count, err := facades.Orm().Query().Model(&models.Setting{}).Where("key = ?", key).Count()
	s.Require().NoError(err)
	return count
}
