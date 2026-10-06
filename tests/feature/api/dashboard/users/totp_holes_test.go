package users

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// TotpHolesTestSuite covers the three TOTP gaps left after S4: disabling
// without a live code, re-running setup while 2FA is already on, and reusing
// a login code for a withdrawal.
type TotpHolesTestSuite struct {
	authSuite
}

func TestTotpHolesSuite(t *testing.T) {
	suite.Run(t, new(TotpHolesTestSuite))
}

func (s *TotpHolesTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *TotpHolesTestSuite) storedUser(id uuid.UUID) models.User {
	s.T().Helper()
	var user models.User
	s.Require().NoError(facades.Orm().Query().Where("id = ?", id).First(&user))
	s.Require().NotEqual(uuid.Nil, user.ID)
	return user
}

func (s *TotpHolesTestSuite) signedInWithTOTP(user seededAuthUser) loginBody {
	s.T().Helper()
	_, challenge := s.loginAs(user.Email)
	s.Require().NotEmpty(challenge.ChallengeToken)
	resp, session := s.verifyTwoFactor(challenge.ChallengeToken, "", user.RecoveryCodes[0])
	resp.AssertOk()
	s.Require().NotEmpty(session.AccessToken)
	return session
}

func (s *TotpHolesTestSuite) TestDisableTOTPWithoutACodeLeavesItOn() {
	user := s.seedUser(true)
	session := s.signedInWithTOTP(user)
	before := s.storedUser(user.ID)

	resp := s.authedDelete(session.AccessToken, "/v1/users/me/totp")

	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{"code": "unauthorized", "message": "invalid 2FA code"}})
	after := s.storedUser(user.ID)
	s.True(after.TotpEnabled)
	s.Equal(before.TotpSecret, after.TotpSecret)
	s.assertSessionWorks(session)
}

func (s *TotpHolesTestSuite) TestDisableTOTPWithAWrongCodeRevokesNothing() {
	user := s.seedUser(true)
	session := s.signedInWithTOTP(user)

	resp := s.authedDeleteJSON(session.AccessToken, "/v1/users/me/totp", `{"code":"000000"}`)

	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{"code": "unauthorized", "message": "invalid 2FA code"}})
	s.True(s.storedUser(user.ID).TotpEnabled)
	s.assertSessionWorks(session)
}

func (s *TotpHolesTestSuite) TestDisableTOTPWithTheCurrentCodeRevokesSessions() {
	user := s.seedUser(true)
	caller := s.signedInWithTOTP(user)
	_, otherChallenge := s.loginAs(user.Email)
	_, otherDevice := s.verifyTwoFactor(otherChallenge.ChallengeToken, "", user.RecoveryCodes[1])
	s.Require().NotEmpty(otherDevice.AccessToken)

	resp := s.authedDeleteJSON(caller.AccessToken, "/v1/users/me/totp", fmt.Sprintf(
		`{"code":%q}`, s.currentCode(user.TOTPSecret),
	))

	resp.AssertOk()
	var body struct {
		User struct {
			TotpEnabled bool `json:"totp_enabled"`
		} `json:"user"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	s.decode(resp, &body)
	s.False(body.User.TotpEnabled)
	s.False(s.storedUser(user.ID).TotpEnabled)
	s.assertSessionRefused(caller)
	s.assertSessionRefused(otherDevice)
	s.assertSessionWorks(loginBody{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken})
}

func (s *TotpHolesTestSuite) TestSetupTOTPWhileActiveIsRejected() {
	user := s.seedUser(true)
	session := s.signedInWithTOTP(user)
	before := s.storedUser(user.ID)

	resp := s.authedPost(session.AccessToken, "/v1/users/me/totp/setup", "")

	resp.AssertStatus(409).AssertJson(map[string]any{"error": map[string]any{"code": "conflict", "message": "2FA is already enabled"}})
	after := s.storedUser(user.ID)
	s.True(after.TotpEnabled)
	s.Equal(before.TotpSecret, after.TotpSecret)
}

func (s *TotpHolesTestSuite) TestCodeConsumedAtLoginIsRejectedForWithdrawal() {
	user := s.seedUser(true)
	_, challenge := s.loginAs(user.Email)
	code := s.currentCode(user.TOTPSecret)
	resp, session := s.verifyTwoFactor(challenge.ChallengeToken, code, "")
	resp.AssertOk()
	s.Require().NotEmpty(session.AccessToken)

	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "totp-holes", Status: models.StatusActive, Environment: "prod",
	}))
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO account_users (id, account_id, user_id, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'owner', ?, NOW(), NOW())`,
		uuid.New(), accountID, user.ID, models.StatusActive,
	)
	s.Require().NoError(err)

	priv, err := btcec.NewPrivateKey()
	s.Require().NoError(err)
	chainCode := sha256.Sum256([]byte("totp-holes-" + user.ID.String()))
	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            "totp holes wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     hex.EncodeToString(priv.PubKey().SerializeCompressed()),
		MPCChainCode:     hex.EncodeToString(chainCode[:]),
		MPCCurve:         "secp256k1",
		AccountID:        &accountID,
	}))

	body := fmt.Sprintf(
		`{"amount":"1","destination_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","passphrase":"test-passphrase-123","totp_code":%q}`,
		code,
	)
	withdrawal := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+session.AccessToken).
		WithHeader("Content-Type", "application/json").
		WithHeader("X-Account-Id", accountID.String())
	withdrawalResp, err := withdrawal.Post("/v1/wallets/"+walletID.String()+"/withdrawals", strings.NewReader(body))
	s.Require().NoError(err)
	withdrawalResp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{"code": "unauthorized", "message": "invalid 2FA code"}})
}
