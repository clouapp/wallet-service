package controllers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"

	authsvc "github.com/macrowallets/waas/app/services/auth"
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
	suite.Suite
	goravelTesting.TestCase
}

func (s *authSuite) seedUser(withTOTP bool) seededAuthUser {
	svc := authsvc.NewService()
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
	sealed, err := facades.Crypt().EncryptString(secret)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`UPDATE users SET totp_secret = ?, totp_enabled = TRUE WHERE id = ?`, sealed, user.ID,
	)
	s.Require().NoError(err)
	user.TOTPSecret = secret

	codes, hashes, err := svc.GenerateRecoveryCodes()
	s.Require().NoError(err)
	for i, codeHash := range hashes[:2] {
		_, err = facades.Orm().Query().Exec(
			`INSERT INTO totp_recovery_codes (id, user_id, code_hash, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())`,
			uuid.New(), user.ID, codeHash,
		)
		s.Require().NoError(err)
		user.RecoveryCodes = append(user.RecoveryCodes, codes[i])
	}
	return user
}

func (s *authSuite) postJSON(path, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post(path, strings.NewReader(body))
	s.Require().NoError(err)
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
	resp, err := s.Http(s.T()).WithHeader("Authorization", "Bearer "+bearer).Get("/v1/users/me")
	s.Require().NoError(err)
	return resp
}

func (s *authSuite) authedPost(bearer, path, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		WithHeader("Content-Type", "application/json").
		Post(path, strings.NewReader(body))
	s.Require().NoError(err)
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
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+bearer).
		WithHeader("Content-Type", "application/json").
		Delete(path, payload)
	s.Require().NoError(err)
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
	s.Require().NotEmpty(body.AccessToken)
	return body
}
