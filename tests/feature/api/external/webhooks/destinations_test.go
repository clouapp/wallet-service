package webhooks

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/types"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// refusedAccountWebhookURLs are internal destinations and the rule each breaks. The
// suites allow loopback (their receivers are httptest servers), so 127.0.0.1 is
// covered by the guard's own tests.
var refusedAccountWebhookURLs = map[string]string{
	"http://169.254.169.254/latest/meta-data/":             egress.ReasonLinkLocal,
	"https://169.254.170.2/v2/credentials":                 egress.ReasonLinkLocal,
	"https://[fd00:ec2::254]/latest/meta-data/":            egress.ReasonLinkLocal,
	"https://10.0.0.1/hook":                                egress.ReasonPrivate,
	"https://192.168.1.10/hook":                            egress.ReasonPrivate,
	"https://openbao/v1/sys/health":                        egress.ReasonHostName,
	"https://vault.internal/v1/sys/health":                 egress.ReasonHostName,
	"https://metadata.google.internal/computeMetadata/v1/": egress.ReasonMetadata,
	"https://2130706433/hook":                              egress.ReasonHostName,
	"https://0.0.0.0/hook":                                 egress.ReasonReserved,
	"http://hooks.example.com/in":                          egress.ReasonScheme,
	"https://user:secret@hooks.example.com/in":             egress.ReasonUserinfo,
}

func (s *WebhooksControllerTestSuite) TestCreate_Webhook_RefusesAnInternalDestination() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	for url, reason := range refusedAccountWebhookURLs {
		body := fmt.Sprintf(`{"url":%q,"secret":"webhook_secret","events":["deposit.confirmed"]}`, url)
		resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(body)
		s.AssertError(resp, http.StatusUnprocessableEntity, "webhook_url_not_allowed", reason)
	}
	var stored int64
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT count(*) FROM webhook_configs`).Scan(&stored))
	s.Zero(stored, "a refused webhook was stored")
}

// TestUpdate_Webhook_RefusesToKeepAnInternalDestinationActive: a legacy webhook
// stored before the guard cannot be claimed, edited or re-enabled while its URL is
// refused, and can always be turned off.
func (s *WebhooksControllerTestSuite) TestUpdate_Webhook_RefusesToKeepAnInternalDestinationActive() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	const secret = "legacy-internal-secret"
	legacy := fixtures.InsertScopedWebhookConfig(s.T(), "http://169.254.169.254/latest/meta-data/", secret,
		[]string{string(types.EventDepositConfirmed)}, nil, nil)
	path := "/api/v1/webhooks/" + legacy.ID.String()

	claim := s.External(path, token).Patch(`{"events":["deposit.confirmed","withdrawal.confirmed"],"secret":"` + secret + `"}`)
	s.AssertError(claim, http.StatusUnprocessableEntity, "webhook_url_not_allowed", egress.ReasonLinkLocal)
	var owner *string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT account_id::text FROM webhook_configs WHERE id = ?`, legacy.ID).Scan(&owner))
	s.Nil(owner, "a refused claim took the webhook")

	off := s.External(path, token).Patch(`{"is_active":false,"secret":"` + secret + `"}`)
	off.AssertOk()
	content, err := off.Content()
	s.Require().NoError(err)
	var turnedOff map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &turnedOff))
	s.Equal(false, turnedOff["is_active"])

	on := s.External(path, token).Patch(`{"is_active":true}`)
	s.AssertError(on, http.StatusUnprocessableEntity, "webhook_url_not_allowed", egress.ReasonLinkLocal)
}
