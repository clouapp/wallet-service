package features

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
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
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionFeaturesGlobalUpdated))
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionFeaturesUpdated))
}

func (s *featureGateSuite) TestGlobalWithdrawalsFalseBlocksTheNextWithdrawUntilItIsOnAgain() {
	userID, accountID, session, walletID := s.ownerWallet()
	apiToken := s.apiToken(accountID, "global-withdrawals")
	s.grantPlatformAdmin(userID)

	body := s.platformList(session)
	s.Equal(len(features.ForGlobal()), len(body.Features))
	s.True(s.listed(body, features.FlagWithdrawalsEnabled))
	s.True(s.listed(body, features.FlagSweepEnabled))
	s.True(s.listed(body, features.FlagDepositScanEnabled))
	s.True(s.listed(body, features.FlagWalletCreationEnabled))
	s.True(s.listed(body, features.FlagWebhookDeliveryEnabled))
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
	case http.MethodPut:
		response, err = request.Put(path, strings.NewReader(body))
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

func (s *featureGateSuite) TestPlatformAdminReadsOneAccountFeatureScope() {
	userID, accountID, session, _ := s.ownerWallet()
	accountPath := "/v1/platform/features/account/" + accountID.String()

	forbidden := s.platform(session, http.MethodGet, accountPath, "", http.StatusForbidden)
	s.Equal("forbidden", errorCode(forbidden))
	s.Equal(int64(0), s.accountFeatureCount(accountID))

	for _, scope := range []string{"global", "user", "chain"} {
		refused := s.platform(session, http.MethodGet, "/v1/platform/features/"+scope+"/"+accountID.String(), "", http.StatusNotFound)
		s.Equal("not_found", errorCode(refused))
		s.Equal("feature scope not found", errorMessage(refused))
	}
	badID := s.platform(session, http.MethodGet, "/v1/platform/features/account/not-a-uuid", "", http.StatusBadRequest)
	s.Equal("invalid_request", errorCode(badID))
	s.Equal("invalid account id", errorMessage(badID))
	s.Equal(int64(0), s.featureActivityCount())

	s.grantPlatformAdmin(userID)
	unknown := s.platform(session, http.MethodGet, "/v1/platform/features/account/"+uuid.New().String(), "", http.StatusNotFound)
	s.Equal("not_found", errorCode(unknown))
	s.Equal("account not found", errorMessage(unknown))

	s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagWithdrawalsEnabled, `{"enabled":false}`, http.StatusOK)
	beforeRead := s.featureActivityCount()
	listed := s.platformListAt(session, accountPath)
	s.Equal(beforeRead, s.featureActivityCount())
	s.Equal(len(features.ForAccount()), len(listed.Features))
	s.True(s.listed(listed, features.FlagWithdrawalsEnabled))
	s.False(s.listed(listed, features.FlagAPIRequestSignatureRequired))
	s.Equal(int64(0), s.accountFeatureCount(accountID))
	s.False(s.globalEnabled(features.FlagWithdrawalsEnabled))

	s.setFlag(session, accountID, features.FlagWithdrawalsEnabled, false)
	listed = s.platformListAt(session, accountPath)
	s.False(s.listed(listed, features.FlagWithdrawalsEnabled))
	s.Equal(int64(1), s.accountFeatureCount(accountID))
}

func (s *featureGateSuite) TestPlatformAdminWritesOneAccountFeatureScope() {
	userID, accountID, session, _ := s.ownerWallet()
	accountPath := "/v1/platform/features/account/" + accountID.String()
	oneFlag := accountPath + "/" + features.FlagWithdrawalsEnabled
	body := `{"enabled":true}`

	forbidden := s.platform(session, http.MethodPut, oneFlag, body, http.StatusForbidden)
	s.Equal("forbidden", errorCode(forbidden))
	s.Equal(int64(0), s.accountFeatureCount(accountID))
	s.Equal(int64(0), s.globalRowCount())
	s.Equal(int64(0), s.featureActivityCount())

	for _, scope := range []string{"global", "user", "chain"} {
		refused := s.platform(session, http.MethodPut, "/v1/platform/features/"+scope+"/"+accountID.String()+"/"+features.FlagWithdrawalsEnabled, "not-json", http.StatusNotFound)
		s.Equal("not_found", errorCode(refused))
		s.Equal("feature scope not found", errorMessage(refused))
	}
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionUserFeaturesUpdated))
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionChainFeaturesUpdated))
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionAccountFeaturesUpdated))
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionFeaturesGlobalUpdated))
	s.Equal(int64(0), s.featureActivityCount())
	badID := s.platform(session, http.MethodPut, "/v1/platform/features/account/not-a-uuid/"+features.FlagWithdrawalsEnabled, "not-json", http.StatusBadRequest)
	s.Equal("invalid_request", errorCode(badID))
	s.Equal("invalid account id", errorMessage(badID))
	s.Equal(int64(0), s.accountFeatureCount(accountID))

	s.grantPlatformAdmin(userID)
	unknownAccount := s.platform(session, http.MethodPut, "/v1/platform/features/account/"+uuid.New().String()+"/"+features.FlagWithdrawalsEnabled, body, http.StatusNotFound)
	s.Equal("not_found", errorCode(unknownAccount))
	s.Equal("account not found", errorMessage(unknownAccount))

	unknownFlag := s.platform(session, http.MethodPut, accountPath+"/not-a-flag", `{"enabled":false}`, http.StatusNotFound)
	s.Equal("not_found", errorCode(unknownFlag))
	s.Equal("feature not found", errorMessage(unknownFlag))
	s.Equal(int64(0), s.accountFeatureCount(accountID))

	s.platform(session, http.MethodPatch, "/v1/platform/features/"+features.FlagWithdrawalsEnabled, `{"enabled":false}`, http.StatusOK)
	s.Equal(int64(1), s.activityActionCount(activitylog.ActionFeaturesGlobalUpdated))
	s.Equal(int64(0), s.featureActivityCount())
	globalAccountID, globalMeta := s.oneActivity(activitylog.ActionFeaturesGlobalUpdated)
	s.Nil(globalAccountID)
	s.True(globalMeta.Before[features.FlagWithdrawalsEnabled])
	s.False(globalMeta.After[features.FlagWithdrawalsEnabled])

	written := s.platform(session, http.MethodPut, oneFlag, body, http.StatusOK)
	s.Equal(features.FlagWithdrawalsEnabled, written["key"])
	s.Equal(true, written["enabled"])
	s.True(s.accountEnabled(accountID, features.FlagWithdrawalsEnabled))
	s.False(s.globalEnabled(features.FlagWithdrawalsEnabled))
	s.Equal(int64(1), s.accountFeatureCount(accountID))
	s.Equal(int64(0), s.accountFeatureActivityCount(accountID))

	listed := s.platformListAt(session, accountPath)
	s.True(s.listed(listed, features.FlagWithdrawalsEnabled))
	s.False(s.listed(listed, features.FlagAPIRequestSignatureRequired))
	s.Equal(int64(1), s.accountFeatureCount(accountID))

	rejected := s.platform(session, http.MethodPut, accountPath, `{"features":[{"key":"not-a-flag","enabled":false},{"key":"`+features.FlagSweepEnabled+`","enabled":false}]}`, http.StatusNotFound)
	s.Equal("feature not found", errorMessage(rejected))
	s.Equal(int64(1), s.accountFeatureCount(accountID))
	s.Equal(int64(0), s.accountFeatureKeyCount(accountID, features.FlagSweepEnabled))
	s.Equal(int64(0), s.accountFeatureActivityCount(accountID))
	s.Equal(int64(1), s.activityActionCount(activitylog.ActionFeaturesGlobalUpdated))

	parsed := s.platform(session, http.MethodPut, accountPath, `{"features":[{"key":"`+features.FlagWalletCreationEnabled+`","enabled":false},{"key":"`+features.FlagSweepEnabled+`","enabled":false}]}`, http.StatusOK)
	raw, err := json.Marshal(parsed)
	s.Require().NoError(err)
	var saved platformListBody
	s.Require().NoError(json.Unmarshal(raw, &saved))
	s.Equal(2, len(saved.Features))
	s.Equal(features.FlagSweepEnabled, saved.Features[0].Key)
	s.False(saved.Features[0].Enabled)
	s.Equal(features.FlagWalletCreationEnabled, saved.Features[1].Key)
	s.False(saved.Features[1].Enabled)
	s.Equal(int64(3), s.accountFeatureCount(accountID))
	s.True(s.accountEnabled(accountID, features.FlagWithdrawalsEnabled))
	s.False(s.globalEnabled(features.FlagWithdrawalsEnabled))
	s.Equal(int64(1), s.accountFeatureActivityCount(accountID))
	s.Equal(int64(0), s.featureActivityCount())
	auditAccountID, audit := s.oneActivity(activitylog.ActionAccountFeaturesUpdated)
	s.Require().NotNil(auditAccountID)
	s.Equal(accountID.String(), *auditAccountID)
	s.True(audit.Before[features.FlagSweepEnabled])
	s.True(audit.Before[features.FlagWalletCreationEnabled])
	s.False(audit.After[features.FlagSweepEnabled])
	s.False(audit.After[features.FlagWalletCreationEnabled])
	_, unchanged := audit.Before[features.FlagWithdrawalsEnabled]
	s.False(unchanged)
	s.NotContains(s.activityText(activitylog.ActionAccountFeaturesUpdated), "enc:v1:")

	s.platform(session, http.MethodPut, accountPath, `{"features":[{"key":"`+features.FlagWalletCreationEnabled+`","enabled":false},{"key":"`+features.FlagSweepEnabled+`","enabled":false}]}`, http.StatusOK)
	s.Equal(int64(1), s.accountFeatureActivityCount(accountID))

	for _, scope := range []string{"user", "chain"} {
		refused := s.platform(session, http.MethodPut, "/v1/platform/features/"+scope+"/"+accountID.String(), `{"features":[{"key":"`+features.FlagSweepEnabled+`","enabled":true}]}`, http.StatusNotFound)
		s.Equal("not_found", errorCode(refused))
	}
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionUserFeaturesUpdated))
	s.Equal(int64(0), s.activityActionCount(activitylog.ActionChainFeaturesUpdated))
	s.Equal(int64(1), s.accountFeatureActivityCount(accountID))
	s.Equal(int64(1), s.activityActionCount(activitylog.ActionFeaturesGlobalUpdated))
}

func (s *featureGateSuite) accountFeatureActivityCount(accountID uuid.UUID) int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM account_activity WHERE action = ? AND account_id = ?`,
		activitylog.ActionAccountFeaturesUpdated, accountID,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}

type featureAuditMetadata struct {
	Before map[string]bool `json:"before"`
	After  map[string]bool `json:"after"`
}

func (s *featureGateSuite) oneActivity(action string) (*string, featureAuditMetadata) {
	s.T().Helper()
	var row struct {
		AccountID *string `gorm:"column:account_id"`
		Metadata  string  `gorm:"column:metadata"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT account_id::text AS account_id, metadata::text AS metadata FROM account_activity WHERE action = ?`,
		action,
	).Scan(&row)
	s.Require().NoError(err)
	var meta featureAuditMetadata
	s.Require().NoError(json.Unmarshal([]byte(row.Metadata), &meta))
	return row.AccountID, meta
}

func (s *featureGateSuite) activityText(action string) string {
	s.T().Helper()
	var row struct {
		Metadata string `gorm:"column:metadata"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT metadata::text AS metadata FROM account_activity WHERE action = ?`,
		action,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Metadata
}

func (s *featureGateSuite) activityActionCount(action string) int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM account_activity WHERE action = ?`,
		action,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}

func (s *featureGateSuite) accountFeatureKeyCount(accountID uuid.UUID, key string) int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM features WHERE account_id = ? AND "key" = ?`,
		accountID, key,
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
}

func (s *featureGateSuite) platformListAt(token, path string) platformListBody {
	s.T().Helper()
	parsed := s.platform(token, http.MethodGet, path, "", http.StatusOK)
	raw, err := json.Marshal(parsed)
	s.Require().NoError(err)
	var body platformListBody
	s.Require().NoError(json.Unmarshal(raw, &body))
	return body
}

func (s *featureGateSuite) featureActivityCount() int64 {
	s.T().Helper()
	var row struct {
		Count int64 `gorm:"column:count"`
	}
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(*) AS count FROM account_activity WHERE action = ?`,
		"features.updated",
	).Scan(&row)
	s.Require().NoError(err)
	return row.Count
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

func errorMessage(body map[string]any) string {
	errorBody, _ := body["error"].(map[string]any)
	message, _ := errorBody["message"].(string)
	return message
}
