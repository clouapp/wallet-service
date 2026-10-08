package chains

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/pquerna/otp/totp"

	appfacades "github.com/macrowallets/waas/app/facades"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
)

const authTestPassword = "correct-horse-battery-staple"

// seededAuthUser is a dashboard user created straight in the database, with
// the plaintext secrets a test needs to sign in as them.
type seededAuthUser struct {
	ID            uuid.UUID
	Email         string
	TOTPSecret    string
	RecoveryCodes []string
}

type loginBody struct {
	Requires2FA    bool   `json:"requires_2fa"`
	ChallengeToken string `json:"challenge_token"`
	ExpiresIn      int    `json:"expires_in"`
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token"`
	AccountID      string `json:"account_id"`
}

// authSuite carries the helpers shared by the dashboard auth suites.
type authSuite struct {
	support.HTTPSuite
}

func (s *authSuite) seedUser(withTOTP bool) seededAuthUser {
	svc := authsvc.NewService(appfacades.Hash())
	hash, err := svc.HashPassword(authTestPassword)
	s.Require().NoError(err)

	user := seededAuthUser{ID: uuid.New()}
	user.Email = "auth-" + user.ID.String()[:8] + "@example.com"
	// Raw SQL so the NOT NULL preferences column takes its '{}' default.
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', NOW(), NOW())`,
		user.ID, user.Email, hash,
	)
	s.Require().NoError(err)

	if !withTOTP {
		return user
	}

	secret, _, err := svc.GenerateTOTP(user.Email)
	s.Require().NoError(err)
	encrypted, err := facades.Crypt().EncryptString(secret)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`UPDATE users SET totp_enabled = TRUE WHERE id = ?`, user.ID,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, confirmed_at, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, ?, NOW(), 0, NOW(), NOW())`,
		uuid.New(), user.ID, "enc:v1:"+encrypted,
	)
	s.Require().NoError(err)
	user.TOTPSecret = secret

	codes, hashes, err := svc.GenerateRecoveryCodes()
	s.Require().NoError(err)
	for i, codeHash := range hashes[:2] {
		_, err = facades.Orm().Query().Exec(`
			INSERT INTO mfa_backup_codes (
				id, subject_type, subject_id, code_hash, created_at, updated_at
			) VALUES (?, 'users', ?, ?, NOW(), NOW())`,
			uuid.New(), user.ID, codeHash,
		)
		s.Require().NoError(err)
		user.RecoveryCodes = append(user.RecoveryCodes, codes[i])
	}
	return user
}

func (s *authSuite) postJSON(path, body string) contractstesting.Response {
	resp := s.Post(path, support.Session{}, body)
	return resp
}

func (s *authSuite) decode(resp contractstesting.Response, into any) {
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Require().NoError(json.Unmarshal([]byte(content), into), content)
}

func (s *authSuite) loginAs(email string) (contractstesting.Response, loginBody) {
	resp := s.postJSON("/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, authTestPassword))
	var body loginBody
	s.decode(resp, &body)
	return resp, body
}

func (s *authSuite) verifyTwoFactor(partialToken, code, recoveryCode string) (contractstesting.Response, loginBody) {
	resp := s.postJSON("/v1/auth/2fa/verify", fmt.Sprintf(
		`{"challenge_token":%q,"code":%q,"recovery_code":%q}`, partialToken, code, recoveryCode,
	))
	var body loginBody
	s.decode(resp, &body)
	return resp, body
}

func (s *authSuite) currentCode(secret string) string {
	code, err := totp.GenerateCode(secret, time.Now())
	s.Require().NoError(err)
	return code
}

func (s *authSuite) getMe(bearer string) contractstesting.Response {
	resp := s.Get("/v1/users/me", support.Session{AccessToken: bearer})
	return resp
}

func (s *authSuite) authedPost(bearer, path, body string) contractstesting.Response {
	resp := s.Post(path, support.Session{AccessToken: bearer}, body)
	return resp
}

func (s *authSuite) authedDelete(bearer, path string) contractstesting.Response {
	return s.authedDeleteJSON(bearer, path, "")
}

func (s *authSuite) authedDeleteJSON(bearer, path, body string) contractstesting.Response {
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	resp := s.Delete(path, support.Session{AccessToken: bearer}, payload)
	return resp
}

func (s *authSuite) refresh(refreshToken string) (contractstesting.Response, loginBody) {
	resp := s.postJSON("/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, refreshToken))
	var body loginBody
	s.decode(resp, &body)
	return resp, body
}

// signIn completes a password-only login and returns the session.
func (s *authSuite) signIn(email string) loginBody {
	resp, body := s.loginAs(email)
	resp.AssertOk()
	s.NotEmpty(body.AccessToken)
	return body
}

func (s *authSuite) assertSessionRefused(session loginBody) {
	s.assertSessionRevoked(s.getMe(session.AccessToken))
	resp, _ := s.refresh(session.RefreshToken)
	s.AssertError(resp, 401, "unauthorized", "invalid or expired refresh token")
}

func (s *authSuite) assertSessionRevoked(resp contractstesting.Response) {
	s.T().Helper()
	resp.AssertUnauthorized()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal("unauthorized", body.Error.Code)
	s.Equal("session revoked", body.Error.Message)
}

func (s *authSuite) assertSessionWorks(session loginBody) {
	s.Require().NotEmpty(session.AccessToken)
	s.Require().NotEmpty(session.RefreshToken)
	s.getMe(session.AccessToken).AssertOk()
	resp, renewed := s.refresh(session.RefreshToken)
	resp.AssertOk()
	s.NotEmpty(renewed.AccessToken)
}
