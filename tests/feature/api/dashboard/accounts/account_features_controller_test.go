package accounts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountFeaturesPassword = "correct-horse-battery"

type accountFeaturesSuite struct {
	support.HTTPSuite
}

func TestAccount_Features_Suite(t *testing.T) {
	support.RunSuite(t, new(accountFeaturesSuite))
}

func (s *accountFeaturesSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *accountFeaturesSuite) TestGet_Missing_RowUsesCatalogDefault() {
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

func (s *accountFeaturesSuite) TestGet_Account_ListsActiveFlagKeys() {
	accountID, token := s.owner()

	body := s.account(token, accountID)
	s.Equal([]string{
		features.FlagDepositScanEnabled,
		features.FlagSweepEnabled,
		features.FlagWalletCreationEnabled,
		features.FlagWebhookDeliveryEnabled,
		features.FlagWithdrawalsEnabled,
	}, body.Features)
	s.Equal(int64(0), s.rowCount(accountID))

	_, err := facades.Orm().Query().Exec(
		`INSERT INTO features (account_id, "key", enabled, created_at, updated_at)
		 VALUES (?, ?, false, NOW(), NOW())`,
		accountID, features.FlagWithdrawalsEnabled,
	)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO global_features ("key", enabled, created_at, updated_at)
		 VALUES (?, false, NOW(), NOW())`,
		features.FlagSweepEnabled,
	)
	s.Require().NoError(err)

	want := []string{
		features.FlagDepositScanEnabled,
		features.FlagSweepEnabled,
		features.FlagWalletCreationEnabled,
		features.FlagWebhookDeliveryEnabled,
	}
	body = s.account(token, accountID)
	s.Equal(want, body.Features)
	s.Equal(want, s.account(s.member(accountID, "auditor"), accountID).Features)

	updated := s.renameAccount(token, accountID)
	s.NotContains(updated, `"features"`)
}

func (s *accountFeaturesSuite) TestAccount_Routes_CannotWriteAFlag() {
	accountID, owner := s.owner()
	admin := s.member(accountID, "admin")
	auditor := s.member(accountID, "auditor")
	user := s.member(accountID, "user")

	body := s.get(admin, accountID, 200)
	s.True(s.flag(body, features.FlagSweepEnabled))
	body = s.get(auditor, accountID, 200)
	s.True(s.flag(body, features.FlagSweepEnabled))
	s.get(user, accountID, 403)

	callers := []string{owner, admin, auditor, user}
	keys := []string{features.FlagWithdrawalsEnabled, "not-a-flag", ""}
	for _, token := range callers {
		for _, key := range keys {
			s.refuseWrite(http.MethodPost, token, accountID, key)
			s.refuseWrite(http.MethodPut, token, accountID, key)
			s.refuseWrite(http.MethodPatch, token, accountID, key)
		}
	}

	s.Equal(int64(0), s.rowCount(accountID))
	body = s.get(owner, accountID, 200)
	s.True(s.flag(body, features.FlagWithdrawalsEnabled))
	s.True(s.flag(body, features.FlagSweepEnabled))
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
	resp := s.Post("/v1/auth/login", support.Session{}, body)
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

type accountDetailBody struct {
	Name     string   `json:"name"`
	Features []string `json:"features"`
}

func (s *accountFeaturesSuite) account(token string, accountID uuid.UUID) accountDetailBody {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String(), support.Session{AccessToken: token})
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed accountDetailBody
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.NotEmpty(parsed.Name)
	return parsed
}

func (s *accountFeaturesSuite) renameAccount(token string, accountID uuid.UUID) string {
	s.T().Helper()
	resp := s.Patch("/v1/accounts/"+accountID.String(), support.Session{AccessToken: token}, `{"name":"Renamed Features"}`)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"name":"Renamed Features"`)
	return content
}

func (s *accountFeaturesSuite) get(token string, accountID uuid.UUID, status int) featureListBody {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/features", support.Session{AccessToken: token})
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status != http.StatusOK {
		s.NotContains(content, features.FlagWithdrawalsEnabled)
		s.NotContains(content, features.FlagSweepEnabled)
		s.NotContains(content, features.FlagDepositScanEnabled)
		s.AssertError(resp, status, "forbidden", features.ErrViewForbidden.Error())
		var denied struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Features json.RawMessage `json:"features"`
		}
		s.Require().NoError(json.Unmarshal([]byte(content), &denied))
		s.Empty(denied.Features)
		return featureListBody{}
	}
	var parsed featureListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	return parsed
}

func (s *accountFeaturesSuite) refuseWrite(method, token string, accountID uuid.UUID, key string) {
	s.T().Helper()
	path := "/v1/accounts/" + accountID.String() + "/features"
	if key != "" {
		path += "/" + key
	}
	session := support.Session{AccessToken: token}
	body := `{"enabled":false}`
	var resp contractstestinghttp.Response
	switch method {
	case http.MethodPost:
		resp = s.Post(path, session, body)
	case http.MethodPut:
		resp = s.Put(path, session, body)
	case http.MethodPatch:
		resp = s.Patch(path, session, body)
	default:
		s.FailNow("unsupported method " + method)
		return
	}
	resp.AssertNotFound()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "404 page not found")
	s.Equal(int64(0), s.rowCount(accountID))
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
