package controllers_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/mocks"
)

const accountFeaturesPassword = "correct-horse-battery"

type accountFeaturesSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountFeaturesSuite(t *testing.T) {
	suite.Run(t, new(accountFeaturesSuite))
}

func (s *accountFeaturesSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *accountFeaturesSuite) TestGetMissingRowUsesCatalogDefault() {
	accountID, token := s.owner()

	body := s.get(token, accountID, 200)
	s.Equal(len(features.All()), len(body.Features))
	s.True(s.flag(body, features.FlagWithdrawalsEnabled))
	s.True(s.flag(body, features.FlagSweepEnabled))
	s.True(s.flag(body, features.FlagDepositScanEnabled))
	s.True(s.flag(body, features.FlagWalletCreationEnabled))
	s.True(s.flag(body, features.FlagWebhookDeliveryEnabled))
	s.False(s.flag(body, features.FlagAPIRequestSignatureRequired))
	s.False(s.flag(body, features.FlagUser2FARequired))
	s.Equal(int64(0), s.rowCount(accountID))
}

func (s *accountFeaturesSuite) TestOwnerEnablesFlagAndTheNextReadIsEnabled() {
	accountID, token := s.owner()

	written := s.patch(token, accountID, features.FlagWithdrawalsEnabled, `{"enabled":true}`, 200)
	s.Equal(features.FlagWithdrawalsEnabled, written["key"])
	s.Equal(true, written["enabled"])
	s.True(s.stored(accountID, features.FlagWithdrawalsEnabled))

	body := s.get(token, accountID, 200)
	s.True(s.flag(body, features.FlagWithdrawalsEnabled))
	s.True(s.flag(body, features.FlagSweepEnabled))

	disabled := s.patch(token, accountID, features.FlagWithdrawalsEnabled, `{"enabled":false}`, 200)
	s.Equal(false, disabled["enabled"])
	s.False(s.stored(accountID, features.FlagWithdrawalsEnabled))
	body = s.get(token, accountID, 200)
	s.False(s.flag(body, features.FlagWithdrawalsEnabled))
	s.True(s.flag(body, features.FlagSweepEnabled))

	otherID, otherToken := s.owner()
	other := s.get(otherToken, otherID, 200)
	s.True(s.flag(other, features.FlagWithdrawalsEnabled))
	s.Equal(int64(0), s.rowCount(otherID))
}

func (s *accountFeaturesSuite) TestAuditorPatchIsForbidden() {
	accountID, _ := s.owner()
	token := s.member(accountID, "auditor")

	body := s.get(token, accountID, 200)
	s.True(s.flag(body, features.FlagSweepEnabled))

	response := s.patch(token, accountID, features.FlagSweepEnabled, `{"enabled":true}`, 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
	s.Equal(int64(0), s.rowCount(accountID))
}

func (s *accountFeaturesSuite) TestUserPatchIsForbidden() {
	accountID, _ := s.owner()
	token := s.member(accountID, "user")

	s.get(token, accountID, 403)
	response := s.patch(token, accountID, features.FlagSweepEnabled, `{"enabled":true}`, 403)
	s.Equal("forbidden", response["error"].(map[string]any)["code"])
	s.Equal(int64(0), s.rowCount(accountID))
}

func (s *accountFeaturesSuite) TestUnknownKeyIsNotFound() {
	accountID, token := s.owner()

	response := s.patch(token, accountID, "not-a-flag", `{"enabled":true}`, 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
	s.Equal(int64(0), s.rowCount(accountID))

	auditor := s.member(accountID, "auditor")
	response = s.patch(auditor, accountID, "not-a-flag", `{"enabled":true}`, 404)
	s.Equal("not_found", response["error"].(map[string]any)["code"])
}

func (s *accountFeaturesSuite) owner() (uuid.UUID, string) {
	s.T().Helper()
	userID, token := s.user("owner")
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "Features " + accountID.String()[:8],
		Status:      "active",
		Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner",
	}))
	return accountID, token
}

func (s *accountFeaturesSuite) member(accountID uuid.UUID, role string) string {
	s.T().Helper()
	userID, token := s.user(role)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role,
	}))
	return token
}

func (s *accountFeaturesSuite) user(role string) (uuid.UUID, string) {
	s.T().Helper()
	hash, err := authsvc.NewService().HashPassword(accountFeaturesPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID, s.login(email)
}

func (s *accountFeaturesSuite) login(email string) string {
	s.T().Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountFeaturesPassword)
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

type featureListBody struct {
	Features []struct {
		Key     string `json:"key"`
		Enabled bool   `json:"enabled"`
	} `json:"features"`
}

func (s *accountFeaturesSuite) get(token string, accountID uuid.UUID, status int) featureListBody {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/features")
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status != 200 {
		return featureListBody{}
	}
	var parsed featureListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	return parsed
}

func (s *accountFeaturesSuite) patch(token string, accountID uuid.UUID, key, body string, status int) map[string]any {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/"+accountID.String()+"/features/"+key, strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	return parsed
}

func (s *accountFeaturesSuite) flag(body featureListBody, key string) bool {
	s.T().Helper()
	for _, flag := range body.Features {
		if flag.Key == key {
			return flag.Enabled
		}
	}
	s.Failf("missing flag", "%s in %+v", key, body)
	return false
}

func (s *accountFeaturesSuite) stored(accountID uuid.UUID, key string) bool {
	s.T().Helper()
	var row struct {
		Enabled bool `gorm:"column:enabled"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT enabled FROM features WHERE account_id = ? AND "key" = ?`,
		accountID, key,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Enabled
}

func (s *accountFeaturesSuite) rowCount(accountID uuid.UUID) int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM features WHERE account_id = ?`,
		accountID,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}
