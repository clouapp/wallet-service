package settings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformWebhookDeliveryTestSuite is PUT /v1/platform/settings/webhook_delivery
// from S1.4.4. settings.update is the named permission; a platform_admins row
// is the gate because that name is not in the platform catalog.
type PlatformWebhookDeliveryTestSuite struct {
	authSuite
}

func TestPlatform_Webhook_DeliverySuite(t *testing.T) {
	support.RunSuite(t, new(PlatformWebhookDeliveryTestSuite))
}

func (s *PlatformWebhookDeliveryTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
	settings.FacadeCache{}.Forget("settings:platform:webhook_delivery")
}

func (s *PlatformWebhookDeliveryTestSuite) TestA_Stored_LimitAndTimeoutAreWhatDeliveryUses() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	accountID := s.seedOwnedAccount(admin.ID)
	beforeChain := s.loadChain(models.ChainETH)
	beforeRPC := beforeChain.RpcURL
	beforeGas := beforeChain.GasReadinessThreshold().String()
	beforeDust := deref(beforeChain.DustThresholdNativeRaw)
	beforeConfirmations := beforeChain.RequiredConfirmations

	var hit atomic.Bool
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Store(true)
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	cfg := fixtures.InsertScopedWebhookConfig(s.T(), server.URL, "delivery-settings-secret", []string{"withdrawal.broadcast"}, &accountID, nil)
	sealedBefore := s.sealedSecret(cfg.ID)
	if !strings.HasPrefix(sealedBefore, "enc:v1:") || strings.Contains(sealedBefore, "delivery-settings-secret") {
		s.Fail("webhook secret was not sealed")
	}

	resp := s.putRaw(session.AccessToken, "/v1/platform/settings/webhook_delivery", `{"max_attempts":1,"timeout_seconds":1}`)
	resp.AssertOk()
	var view struct {
		Name   string `json:"name"`
		Fields []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"fields"`
	}
	s.decode(resp, &view)
	s.Equal("webhook_delivery", view.Name)
	s.Equal(float64(1), settingsField(view.Fields, "max_attempts"))
	s.Equal(float64(1), settingsField(view.Fields, "timeout_seconds"))

	if s.sealedSecret(cfg.ID) != sealedBefore {
		s.Fail("sealed webhook secret changed")
	}
	afterChain := s.loadChain(models.ChainETH)
	if afterChain.RpcURL != beforeRPC {
		s.Fail("chain rpc changed")
	}
	s.Equal(beforeGas, afterChain.GasReadinessThreshold().String())
	s.Equal(beforeDust, deref(afterChain.DustThresholdNativeRaw))
	s.Equal(beforeConfirmations, afterChain.RequiredConfirmations)

	s.Equal("1", s.settingValue("max_attempts"))
	s.Equal("1", s.settingValue("timeout_seconds"))
	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'settings.updated' AND target_type = 'settings' AND target_id = 'webhook_delivery'
		   AND account_id IS NULL AND actor_user_id = ?
		   AND metadata = '{"fields":["max_attempts","timeout_seconds"],"group":"webhook_delivery"}'::jsonb`,
		admin.ID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'platform.secret_viewed'`))
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM account_activity WHERE action = 'settings.updated' AND account_id IS NOT NULL`,
	))

	rejected := s.putRaw(session.AccessToken, "/v1/platform/settings/webhook_delivery", `{"max_attempts":0,"timeout_seconds":-1}`)
	rejected.AssertUnprocessableEntity()
	s.assertValidation(rejected, "max_attempts", "must be between 1 and 20")
	s.Equal("1", s.settingValue("max_attempts"))
	s.Equal("1", s.settingValue("timeout_seconds"))
	s.Equal(int64(1), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))

	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &accountID)
	svc := container.Get().WebhookService
	enqueued, err := svc.EnqueueScoped(context.Background(), webhook.ScopedEvent{
		EventType: types.EventWithdrawalBroadcast,
		SubjectID: uuid.NewString(),
		WalletID:  wallet.ID,
		AccountID: &accountID,
		Data:      map[string]string{"withdrawal_id": "delivery-settings"},
	})
	s.Require().NoError(err)
	s.Equal(1, enqueued)
	var stamped int
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT max_attempts FROM webhook_events WHERE webhook_config_id = ?`, cfg.ID,
	).Scan(&stamped))
	s.Equal(1, stamped)

	delivered, err := svc.DeliverPending(context.Background(), 10)
	s.Require().NoError(err)
	s.Equal(0, delivered)
	s.True(hit.Load())
	s.Equal(models.WebhookDeliveryFailed, s.deliveryStatus(cfg.ID))
	s.Equal(1, s.deliveryAttempts(cfg.ID))
	if s.sealedSecret(cfg.ID) != sealedBefore {
		s.Fail("delivery changed the sealed webhook secret")
	}

	accountActivity := s.accountActivity(session.AccessToken, accountID)
	accountActivity.AssertOk()
	var accountRows struct {
		Data []struct {
			Action string `json:"action"`
		} `json:"data"`
	}
	s.decode(accountActivity, &accountRows)
	for _, row := range accountRows.Data {
		s.NotEqual("settings.updated", row.Action)
	}
}

func (s *PlatformWebhookDeliveryTestSuite) TestA_Missing_RowKeepsTheDefault() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	accountID := s.seedOwnedAccount(admin.ID)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	cfg := fixtures.InsertScopedWebhookConfig(s.T(), receiver.URL, "delivery-default-secret", []string{"withdrawal.broadcast"}, &accountID, nil)
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &accountID)
	svc := container.Get().WebhookService
	enqueued, err := svc.EnqueueScoped(context.Background(), webhook.ScopedEvent{
		EventType: types.EventWithdrawalBroadcast,
		SubjectID: uuid.NewString(),
		WalletID:  wallet.ID,
		AccountID: &accountID,
		Data:      map[string]string{"withdrawal_id": "delivery-default"},
	})
	s.Require().NoError(err)
	s.Equal(1, enqueued)
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'webhook_delivery'`,
	))
	var stamped int
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT max_attempts FROM webhook_events WHERE webhook_config_id = ?`, cfg.ID,
	).Scan(&stamped))
	s.Equal(10, stamped)

	delivered, err := svc.DeliverPending(context.Background(), 10)
	s.Require().NoError(err)
	s.Equal(1, delivered)
	s.Equal(models.WebhookDeliveryDelivered, s.deliveryStatus(cfg.ID))
}

func (s *PlatformWebhookDeliveryTestSuite) TestZero_Or_NegativeIsRejectedAndANonAdminIsForbidden() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)

	missing := s.putRaw(session.AccessToken, "/v1/platform/settings/no-such-group", `{"max_attempts":1}`)
	missing.AssertNotFound()
	s.AssertError(missing, 404, responses.CodeNotFound, "settings group not found")

	forbidden := s.putRaw(session.AccessToken, "/v1/platform/settings/webhook_delivery", `{"max_attempts":4,"timeout_seconds":8}`)
	forbidden.AssertForbidden()
	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to update settings")
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'webhook_delivery'`,
	))

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	adminSession := s.signIn(admin.Email)
	adminMissing := s.putRaw(adminSession.AccessToken, "/v1/platform/settings/account_security", `{"require_2fa":true}`)
	s.AssertError(adminMissing, 404, responses.CodeNotFound, "settings group not found")

	for _, body := range []string{
		`{"max_attempts":0,"timeout_seconds":8}`,
		`{"max_attempts":-1,"timeout_seconds":8}`,
		`{"max_attempts":21,"timeout_seconds":8}`,
		`{"max_attempts":4,"timeout_seconds":0}`,
		`{"max_attempts":4,"timeout_seconds":-2}`,
	} {
		rejected := s.putRaw(adminSession.AccessToken, "/v1/platform/settings/webhook_delivery", body)
		rejected.AssertUnprocessableEntity()
		s.AssertError(rejected, 422, responses.CodeValidationFailed, "validation failed")
		var parsed struct {
			Errors map[string][]string `json:"errors"`
		}
		s.decode(rejected, &parsed)
		s.NotEmpty(parsed.Errors)
	}
	s.Equal(int64(0), s.count(
		`SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = 'webhook_delivery'`,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE action = 'settings.updated'`))
}

func (s *PlatformWebhookDeliveryTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformWebhookDeliveryTestSuite) seedOwnedAccount(userID uuid.UUID) uuid.UUID {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID: accountID, Name: "delivery-" + accountID.String()[:8], Status: models.StatusActive, Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}))
	return accountID
}

func (s *PlatformWebhookDeliveryTestSuite) loadChain(id string) *models.Chain {
	s.T().Helper()
	var chain models.Chain
	s.Require().NoError(facades.Orm().Query().Where("id", id).First(&chain))
	s.Require().Equal(id, chain.ID)
	return &chain
}

func (s *PlatformWebhookDeliveryTestSuite) putRaw(token, path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Put(path, support.Session{AccessToken: token}, body)
	return resp
}

func (s *PlatformWebhookDeliveryTestSuite) accountActivity(bearer string, accountID uuid.UUID) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/activity", support.Session{AccessToken: bearer})
	return resp
}

func (s *PlatformWebhookDeliveryTestSuite) count(query string, args ...any) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(query, args...).Scan(&total))
	return total
}

func (s *PlatformWebhookDeliveryTestSuite) settingValue(key string) string {
	s.T().Helper()
	var value string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT value FROM settings WHERE account_id IS NULL AND "group" = 'webhook_delivery' AND "key" = ?`,
		key,
	).Scan(&value))
	return value
}

func (s *PlatformWebhookDeliveryTestSuite) sealedSecret(id uuid.UUID) string {
	s.T().Helper()
	var secret string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT secret FROM webhook_configs WHERE id = ?`, id,
	).Scan(&secret))
	return secret
}

func (s *PlatformWebhookDeliveryTestSuite) deliveryStatus(id uuid.UUID) string {
	s.T().Helper()
	var status string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT delivery_status FROM webhook_events WHERE webhook_config_id = ?`, id,
	).Scan(&status))
	return status
}

func (s *PlatformWebhookDeliveryTestSuite) deliveryAttempts(id uuid.UUID) int {
	s.T().Helper()
	var attempts int
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT attempts FROM webhook_events WHERE webhook_config_id = ?`, id,
	).Scan(&attempts))
	return attempts
}

func (s *PlatformWebhookDeliveryTestSuite) assertValidation(resp contractstesting.Response, field, message string) {
	s.T().Helper()
	s.AssertError(resp, 422, responses.CodeValidationFailed, "validation failed")
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	s.decode(resp, &body)
	s.Equal([]string{message}, body.Errors[field])
}

func settingsField(fields []struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}, key string) any {
	for _, field := range fields {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
