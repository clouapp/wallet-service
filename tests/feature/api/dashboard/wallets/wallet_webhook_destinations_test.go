package wallets

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// refusedWalletWebhookURLs are internal destinations a wallet webhook was registered
// with on the dev host, and the rule each breaks. The suites allow loopback (their
// receivers are httptest servers), so 127.0.0.1 is covered by the guard's own tests.
var refusedWalletWebhookURLs = map[string]string{
	"http://169.254.169.254/latest/meta-data/iam/security-credentials/": egress.ReasonLinkLocal,
	"http://169.254.170.2/v2/credentials":                               egress.ReasonLinkLocal,
	"http://172.17.0.1:8200/v1/sys/health":                              egress.ReasonPrivate,
	"https://10.0.0.1/hook":                                             egress.ReasonPrivate,
	"https://100.64.0.1/hook":                                           egress.ReasonPrivate,
	"http://openbao:8200/v1/sys/health":                                 egress.ReasonHostName,
	"http://wallet-service:2002/health":                                 egress.ReasonHostName,
	"http://macro-markets-back/api/webhooks":                            egress.ReasonHostName,
	"https://metadata.google.internal/computeMetadata/v1/":              egress.ReasonMetadata,
	"https://[::ffff:169.254.169.254]/latest/meta-data/":                egress.ReasonLinkLocal,
	"https://0xa9fea9fe/latest/meta-data/":                              egress.ReasonHostName,
	"http://hooks.example.com/in":                                       egress.ReasonScheme,
	"https://user:secret@hooks.example.com/in":                          egress.ReasonUserinfo,
}

func (s *WalletWebhookCreateGateTestSuite) TestCreate_Wallet_WebhookRefusesAnInternalDestination() {
	account := fixtures.InsertAccount(s.T(), "wallet webhook destinations")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)

	for url, reason := range refusedWalletWebhookURLs {
		resp := s.createWebhook(owner.token, account.ID, wallet.ID, url)
		s.AssertError(resp, http.StatusUnprocessableEntity, "webhook_url_not_allowed", reason)
	}
	s.Equal(int64(0), s.webhookCount(wallet.ID, ""))
}

func (s *WalletWebhookCreateGateTestSuite) TestWallet_Webhook_TestOfAStoredInternalDestinationSendsNothing() {
	account := fixtures.InsertAccount(s.T(), "wallet webhook stored destination")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)
	// A webhook stored before the guard, as on the dev host.
	stored := fixtures.InsertScopedWebhookConfig(s.T(), "http://169.254.169.254/latest/meta-data/", "stored-secret",
		[]string{string(types.EventDepositConfirmed)}, nil, &wallet.ID)

	resp := s.testWebhook(owner.token, account.ID, wallet.ID, stored.ID)
	s.AssertError(resp, http.StatusUnprocessableEntity, "webhook_url_not_allowed", egress.ReasonLinkLocal)
}

// TestWallet_Webhook_TestAnswersOnlyWhetherItWasDelivered proves webhook.test is no
// read channel: whatever the receiver answers, the caller gets delivered: true or
// the fixed 502, never the receiver's body or headers.
func (s *WalletWebhookCreateGateTestSuite) TestWallet_Webhook_TestAnswersOnlyWhetherItWasDelivered() {
	account := fixtures.InsertAccount(s.T(), "wallet webhook test answer")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)

	var status atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-Receiver-Token", "receiver-header-secret")
		w.WriteHeader(int(status.Load()))
		_, _ = fmt.Fprint(w, `{"AccessKeyId":"receiver-body-secret","Token":"receiver-token-secret"}`)
	}))
	s.T().Cleanup(server.Close)
	created := s.createWebhook(owner.token, account.ID, wallet.ID, server.URL+"/hook")
	created.AssertStatus(http.StatusCreated)
	webhookID := s.createdWebhookID(created)

	status.Store(http.StatusOK)
	delivered := s.testWebhook(owner.token, account.ID, wallet.ID, webhookID)
	delivered.AssertOk()
	s.Equal(`{"delivered":true}`, s.body(delivered))
	s.Empty(delivered.Headers().Get("X-Receiver-Token"))

	status.Store(http.StatusInternalServerError)
	refused := s.testWebhook(owner.token, account.ID, wallet.ID, webhookID)
	s.AssertError(refused, http.StatusBadGateway, "provider_unavailable", "webhook test delivery failed")
	s.Equal(`{"error":{"code":"provider_unavailable","message":"webhook test delivery failed"}}`, s.body(refused))
	s.Empty(refused.Headers().Get("X-Receiver-Token"))
}
