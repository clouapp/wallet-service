package controllers_test

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
)

func (s *featureGateSuite) TestNonAdminCannotReadOrWritePlatformFeatures() {
	_, accountID, session, _ := s.ownerWallet()

	forbidden := s.platform(session, http.MethodGet, "/v1/platform/features", "", http.StatusForbidden)
	s.Equal("forbidden", errorCode(forbidden))

	forbidden = s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagWithdrawalsEnabled, `{"enabled":false}`, http.StatusForbidden)
	s.Equal("forbidden", errorCode(forbidden))
	s.Equal(int64(0), s.globalRowCount())
	s.Equal(int64(0), s.accountFeatureCount(accountID))
}

func (s *featureGateSuite) TestGlobalWithdrawalsFalseBlocksTheNextWithdrawUntilItIsOnAgain() {
	userID, accountID, session, walletID := s.ownerWallet()
	apiToken := s.apiToken(accountID, "global-withdrawals")
	s.grantPlatformAdmin(userID)

	body := s.platformList(session)
	s.Equal(len(features.ForGlobal()), len(body.Features))
	s.True(s.listed(body, features.FlagWithdrawalsEnabled))
	s.True(s.listed(body, features.FlagSweepEnabled))
	s.False(s.listed(body, features.FlagDepositScanEnabled))
	s.False(s.listed(body, features.FlagWalletCreationEnabled))
	s.False(s.listed(body, features.FlagWebhookDeliveryEnabled))
	s.False(s.listed(body, features.FlagAPIRequestSignatureRequired))
	s.False(s.listed(body, features.FlagUser2FARequired))
	s.Equal(int64(0), s.globalRowCount())

	unknown := s.platform(session, http.MethodPatch, "/v1/platform/features/not-a-flag", `{"enabled":false}`, http.StatusNotFound)
	s.Equal("not_found", errorCode(unknown))
	s.Equal(int64(0), s.globalRowCount())

	s.setFlag(session, accountID, features.FlagWithdrawalsEnabled, true)
	s.True(s.accountEnabled(accountID, features.FlagWithdrawalsEnabled))

	surfaces := []gateSurface{
		{name: "dashboard", token: session, prefix: "/v1/wallets/", header: true},
		{name: "external", token: apiToken, prefix: "/api/v1/wallets/", header: false},
	}

	written := s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagWithdrawalsEnabled, `{"enabled":false}`, http.StatusOK)
	s.Equal(false, written["enabled"])
	s.False(s.globalEnabled(features.FlagWithdrawalsEnabled))
	s.True(s.accountEnabled(accountID, features.FlagWithdrawalsEnabled))
	for _, surface := range surfaces {
		beforeWithdrawals := s.rows(&models.Withdrawal{}, walletID)
		beforeTransactions := s.rows(&models.Transaction{}, walletID)
		response := s.post(surface, accountID, walletID, "/withdrawals")
		s.Equal(http.StatusConflict, s.status(response), surface.name+" global off")
		errorBody, _ := s.json(response)["error"].(map[string]any)
		s.Equal(features.CodeWithdrawalsPaused, errorBody["code"], surface.name)
		s.Equal(features.CodeWithdrawalsPaused, errorBody["message"], surface.name)
		s.Equal(beforeWithdrawals, s.rows(&models.Withdrawal{}, walletID), surface.name+" broadcast a withdrawal")
		s.Equal(beforeTransactions, s.rows(&models.Transaction{}, walletID), surface.name+" broadcast a transaction")
	}

	released := s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagWithdrawalsEnabled, `{"enabled":true}`, http.StatusOK)
	s.Equal(true, released["enabled"])
	s.openWithoutBroadcast("global on", surfaces, accountID, walletID, "/withdrawals", features.CodeWithdrawalsPaused)

	s.setFlag(session, accountID, features.FlagWithdrawalsEnabled, false)
	for _, surface := range surfaces {
		response := s.post(surface, accountID, walletID, "/withdrawals")
		s.Equal(http.StatusConflict, s.status(response), surface.name+" account off")
		errorBody, _ := s.json(response)["error"].(map[string]any)
		s.Equal(features.CodeWithdrawalsPaused, errorBody["code"], surface.name)
	}
}

func (s *featureGateSuite) TestGlobalSweepFalseBlocksConsolidateAndAccountOffStillBlocksWhenGlobalIsOn() {
	userID, accountID, session, walletID := s.ownerWallet()
	apiToken := s.apiToken(accountID, "global-sweep")
	s.grantPlatformAdmin(userID)
	s.setFlag(session, accountID, features.FlagSweepEnabled, true)

	surfaces := []gateSurface{
		{name: "dashboard", token: session, prefix: "/v1/wallets/", header: true},
		{name: "external", token: apiToken, prefix: "/api/v1/wallets/", header: false},
	}

	s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagSweepEnabled, `{"enabled":false}`, http.StatusOK)
	for _, surface := range surfaces {
		beforeTransactions := s.rows(&models.Transaction{}, walletID)
		response := s.post(surface, accountID, walletID, "/consolidate")
		s.Equal(http.StatusConflict, s.status(response), surface.name+" global sweep off")
		errorBody, _ := s.json(response)["error"].(map[string]any)
		s.Equal(features.CodeSweepPaused, errorBody["code"], surface.name)
		s.Equal(beforeTransactions, s.rows(&models.Transaction{}, walletID), surface.name+" broadcast a sweep")
	}

	s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagSweepEnabled, `{"enabled":true}`, http.StatusOK)
	s.openWithoutBroadcast("global sweep on", surfaces, accountID, walletID, "/consolidate", features.CodeSweepPaused)

	s.setFlag(session, accountID, features.FlagSweepEnabled, false)
	for _, surface := range surfaces {
		response := s.post(surface, accountID, walletID, "/consolidate")
		s.Equal(http.StatusConflict, s.status(response), surface.name+" account sweep off")
		errorBody, _ := s.json(response)["error"].(map[string]any)
		s.Equal(features.CodeSweepPaused, errorBody["code"], surface.name)
	}
}

func (s *featureGateSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *featureGateSuite) platform(token, method, path, body string, status int) map[string]any {
	s.T().Helper()
	request := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json")
	var (
		response contractstestinghttp.Response
		err      error
	)
	switch method {
	case http.MethodGet:
		response, err = request.Get(path)
	case http.MethodPatch:
		response, err = request.Patch(path, strings.NewReader(body))
	default:
		s.T().Fatalf("unsupported method %s", method)
	}
	s.Require().NoError(err)
	response.AssertStatus(status)
	content, err := response.Content()
	s.Require().NoError(err)
	var parsed map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	return parsed
}

type platformListBody struct {
	Features []struct {
		Key     string `json:"key"`
		Enabled bool   `json:"enabled"`
	} `json:"features"`
}

func (s *featureGateSuite) platformList(token string) platformListBody {
	s.T().Helper()
	parsed := s.platform(token, http.MethodGet, "/v1/platform/features", "", http.StatusOK)
	raw, err := json.Marshal(parsed)
	s.Require().NoError(err)
	var body platformListBody
	s.Require().NoError(json.Unmarshal(raw, &body))
	return body
}

func (s *featureGateSuite) listed(body platformListBody, key string) bool {
	s.T().Helper()
	for _, flag := range body.Features {
		if flag.Key == key {
			return flag.Enabled
		}
	}
	s.Failf("missing flag", "%s", key)
	return false
}

func (s *featureGateSuite) globalRowCount() int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(`SELECT COUNT(*) AS count FROM global_features`).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}

func (s *featureGateSuite) globalEnabled(key string) bool {
	s.T().Helper()
	var row struct {
		Enabled bool `gorm:"column:enabled"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT enabled FROM global_features WHERE "key" = ?`,
		key,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Enabled
}

func (s *featureGateSuite) accountEnabled(accountID uuid.UUID, key string) bool {
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

func (s *featureGateSuite) accountFeatureCount(accountID uuid.UUID) int64 {
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

func errorCode(body map[string]any) string {
	errorBody, _ := body["error"].(map[string]any)
	code, _ := errorBody["code"].(string)
	return code
}
