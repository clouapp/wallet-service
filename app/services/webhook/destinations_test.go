package webhook

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/fakedns"
)

// testDNS answers two names with internal addresses; any other name does not
// resolve (and is left to the dial), so no test asks the network.
var testDNS = mustServeDNS(map[string][]string{
	"private.example.com":  {"10.0.0.7"},
	"metadata.example.com": {"169.254.169.254"},
})

func mustServeDNS(answers map[string][]string) *fakedns.Server {
	server, err := fakedns.Serve(answers)
	if err != nil {
		panic(err)
	}
	return server
}

// testDestinations is the guard of these tests: the webhook policy, with loopback
// allowed for the httptest receivers, over testDNS.
func testDestinations() *egress.Guard {
	return egress.NewGuard(egress.Policy{AllowLoopback: true}, testDNS.Resolver())
}

func TestCreate_Config_RefusesAnInternalDestinationAndStoresNothing(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	accountID := uuid.New()
	cases := map[string]string{
		"http://169.254.169.254/latest/meta-data/":         egress.ReasonLinkLocal,
		"http://169.254.170.2/v2/credentials":              egress.ReasonLinkLocal,
		"https://10.0.0.1/hook":                            egress.ReasonPrivate,
		"https://172.17.0.1:8200/v1/sys/health":            egress.ReasonPrivate,
		"https://openbao/v1/sys/health":                    egress.ReasonHostName,
		"http://wallet-service:2002/health":                egress.ReasonHostName,
		"https://metadata.google.internal/computeMetadata": egress.ReasonMetadata,
		"http://hooks.example.com/in":                      egress.ReasonScheme,
		"https://user:secret@hooks.example.com/in":         egress.ReasonUserinfo,
		"https://0x7f000001/in":                            egress.ReasonHostName,
		"https://private.example.com/hook":                 egress.ReasonResolved,
		"https://metadata.example.com/hook":                egress.ReasonResolved,
	}
	for raw, reason := range cases {
		for _, owner := range []*uuid.UUID{&accountID, nil} {
			cfg, err := svc.CreateConfig(context.Background(), raw, "secret", []string{string(types.EventDepositConfirmed)}, owner)
			if !errors.Is(err, ErrURLNotAllowed) || cfg != nil {
				t.Fatalf("%s: cfg = %v, err = %v, want ErrURLNotAllowed", raw, cfg, err)
			}
			if got := RefusalReason(err); got != reason {
				t.Fatalf("%s: reason = %q, want %q", raw, got, reason)
			}
		}
	}
	if configs, _ := svc.ListConfigs(context.Background()); len(configs) != 0 {
		t.Fatalf("a refused webhook was stored: %v", configs)
	}
}

func TestCreate_Config_StoresAPublicHTTPSDestination(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	cfg, err := svc.CreateConfig(context.Background(), "https://hooks.example.com/in", "secret", []string{string(types.EventDepositConfirmed)}, nil)
	if err != nil || cfg == nil {
		t.Fatalf("CreateConfig = %v, %v", cfg, err)
	}
}

func TestNew_Service_RefusesWithTheStrictPolicyWhenNoGuardIsGiven(t *testing.T) {
	store := newMemoryWebhook()
	svc := NewService(Deps{Configs: store, Events: memoryEvents{store: store}})
	for _, raw := range []string{"http://127.0.0.1:8080/in", "https://127.0.0.1/in", "http://hooks.example.com/in"} {
		if err := svc.CheckURL(context.Background(), raw); !errors.Is(err, ErrURLNotAllowed) {
			t.Fatalf("%s: err = %v, want the strict policy's refusal", raw, err)
		}
	}
}

func TestUpdate_AccountConfig_RefusesToKeepARefusedDestinationActive(t *testing.T) {
	svc, store := newMemoryWebhookService()
	accountID := uuid.New()
	const secret = "legacy-secret"
	legacy := storedConfig(t, store, "http://169.254.169.254/latest/meta-data/", secret, nil)
	owned := storedConfig(t, store, "https://10.0.0.1/hook", secret, &accountID)
	events := []string{string(types.EventDepositConfirmed), string(types.EventWithdrawalConfirmed)}
	active, inactive := true, false

	for name, update := range map[string]ConfigUpdate{
		"claim":            {Events: events, Secret: secret},
		"claim and enable": {IsActive: &active, Secret: secret},
	} {
		if _, err := svc.UpdateAccountConfig(context.Background(), accountID, legacy.ID, update); !errors.Is(err, ErrURLNotAllowed) {
			t.Fatalf("%s: err = %v, want ErrURLNotAllowed", name, err)
		}
	}
	if got, _ := store.FindByID(context.Background(), legacy.ID); got.AccountID != nil || !got.IsActive {
		t.Fatalf("a refused update changed the legacy webhook: %+v", got)
	}
	if _, err := svc.UpdateAccountConfig(context.Background(), accountID, owned.ID, ConfigUpdate{Events: events}); !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("owned events update: err = %v", err)
	}

	// Turning a refused webhook off is always allowed, and claims a legacy one.
	claimed, err := svc.UpdateAccountConfig(context.Background(), accountID, legacy.ID, ConfigUpdate{IsActive: &inactive, Secret: secret})
	if err != nil || claimed.IsActive || claimed.AccountID == nil || *claimed.AccountID != accountID {
		t.Fatalf("disable a refused legacy webhook = %+v, %v", claimed, err)
	}
	if _, err := svc.UpdateAccountConfig(context.Background(), accountID, owned.ID, ConfigUpdate{IsActive: &inactive}); err != nil {
		t.Fatalf("disable a refused webhook: %v", err)
	}
	if _, err := svc.UpdateAccountConfig(context.Background(), accountID, owned.ID, ConfigUpdate{Events: events}); err != nil {
		t.Fatalf("an inactive webhook keeps its events editable: %v", err)
	}
	if _, err := svc.UpdateAccountConfig(context.Background(), accountID, owned.ID, ConfigUpdate{IsActive: &active}); !errors.Is(err, ErrURLNotAllowed) {
		t.Fatalf("enable a refused webhook: err = %v", err)
	}
}

func TestDeliver_ARefusedDestinationIsSettledFailedAndNeverPosted(t *testing.T) {
	logs := captureSlog(t)
	cases := map[string]string{
		// Stored before the guard existed.
		"http://169.254.169.254/latest/meta-data/iam/security-credentials": egress.ReasonLinkLocal,
		// Accepted when it resolved outside, resolving inside at send time.
		"https://private.example.com/hooks/in": egress.ReasonResolved,
	}
	for deliveryURL, reason := range cases {
		svc, store := newMemoryWebhookService()
		cfg := storedConfig(t, store, deliveryURL, "secret", nil)
		eventID := uuid.New()
		if err := store.addEvent(&models.WebhookEvent{ID: eventID, WebhookConfigID: &cfg.ID, EventType: string(types.EventDepositConfirmed),
			Payload: `{"type":"deposit.confirmed"}`, DeliveryURL: deliveryURL, MaxAttempts: 10}); err != nil {
			t.Fatal(err)
		}
		msg := types.WebhookMessage{EventID: eventID.String(), EventType: types.EventDepositConfirmed, Payload: `{"type":"deposit.confirmed"}`,
			DeliveryURL: deliveryURL, ConfigID: cfg.ID.String(), Attempt: 1}

		for attempt := 0; attempt < 2; attempt++ {
			if err := svc.Deliver(context.Background(), msg); err != nil {
				t.Fatalf("%s: Deliver = %v, want the refusal settled so the queue drops it", deliveryURL, err)
			}
		}
		event := store.events[eventID.String()]
		if event.DeliveryStatus != models.WebhookDeliveryFailed || event.LastError != "refused: "+reason || event.Attempts != 0 {
			t.Fatalf("%s: event = %s %q after %d attempts", deliveryURL, event.DeliveryStatus, event.LastError, event.Attempts)
		}
		host := egress.Host(deliveryURL)
		if !strings.Contains(logs.String(), "host="+host) {
			t.Fatalf("the refusal log does not name the host %s:\n%s", host, logs)
		}
	}
	if strings.Contains(logs.String(), "/latest/meta-data") || strings.Contains(logs.String(), "/hooks/in") {
		t.Fatalf("the refusal log carries the URL:\n%s", logs)
	}
}

func TestSendTest_ARefusedDestinationIsNotPosted(t *testing.T) {
	svc, store := newMemoryWebhookService()
	cfg := storedConfig(t, store, "https://metadata.example.com/latest/meta-data/", "secret", nil)
	err := svc.SendTest(context.Background(), cfg, uuid.New())
	if !errors.Is(err, ErrURLNotAllowed) || RefusalReason(err) != egress.ReasonResolved {
		t.Fatalf("SendTest = %v, want the refusal of what the name resolves to", err)
	}
}

func TestSendTest_NeverCarriesTheReceiverAnswer(t *testing.T) {
	svc, store := newMemoryWebhookService()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Internal-Token", "receiver-header-secret")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"AccessKeyId":"receiver-body-secret"}`))
	}))
	defer server.Close()
	cfg := storedConfig(t, store, server.URL+"/hook", "secret", nil)

	err := svc.SendTest(context.Background(), cfg, uuid.New())
	if err == nil {
		t.Fatal("a 500 answer was a delivered test")
	}
	text := err.Error()
	if !strings.Contains(text, "HTTP 500") || strings.Contains(text, "receiver-body-secret") || strings.Contains(text, "receiver-header-secret") {
		t.Fatalf("SendTest error = %q, want the status only", text)
	}
}

func TestAudit_Destinations_ListsTheActiveConfigsTheGuardRefuses(t *testing.T) {
	svc, store := newMemoryWebhookService()
	accountID, walletID := uuid.New(), uuid.New()
	storedConfig(t, store, "https://hooks.example.com/in", "s", &accountID)
	metadata := storedConfig(t, store, "http://169.254.169.254/latest/meta-data/?token=x", "s", nil)
	metadata.WalletID = &walletID
	store.configs[metadata.ID].WalletID = &walletID
	private := storedConfig(t, store, "https://private.example.com/in", "s", &accountID)
	legacy := storedConfig(t, store, "http://openbao:8200/v1/sys", "s", nil)
	inactive := storedConfig(t, store, "https://10.0.0.1/in", "s", &accountID)
	store.setActive(inactive.ID, false)

	audit, err := svc.AuditDestinations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if audit.Active != 4 || len(audit.Refused) != 3 {
		t.Fatalf("audit = %+v, want 4 active and 3 refused", audit)
	}
	want := []RefusedDestination{
		{ConfigID: metadata.ID, Scope: ScopeWallet, Host: "169.254.169.254", Reason: egress.ReasonLinkLocal},
		{ConfigID: private.ID, Scope: ScopeAccount, Host: "private.example.com", Reason: egress.ReasonResolved},
		{ConfigID: legacy.ID, Scope: ScopeLegacy, Host: "openbao", Reason: egress.ReasonHostName},
	}
	for i, refused := range audit.Refused {
		if refused != want[i] {
			t.Fatalf("refused[%d] = %+v, want %+v", i, refused, want[i])
		}
	}
}

func TestAudit_Destinations_ReportsAStoreFailure(t *testing.T) {
	store := failingConfigs{err: errors.New("database down")}
	_, err := NewService(Deps{Configs: store, Destinations: testDestinations()}).AuditDestinations(context.Background())
	if !errors.Is(err, store.err) {
		t.Fatalf("err = %v", err)
	}
}

type failingConfigs struct {
	configStore
	err error
}

func (f failingConfigs) FindActive(context.Context) ([]models.WebhookConfig, error) {
	return nil, f.err
}

// storedConfig writes an active config as a row stored before the guard existed:
// straight to the store, unchecked.
func storedConfig(t *testing.T, store *memoryWebhook, rawURL, secret string, accountID *uuid.UUID) *models.WebhookConfig {
	t.Helper()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: rawURL, Secret: secret, Events: pgArray([]string{string(types.EventDepositConfirmed)}),
		IsActive: true, AccountID: accountID}
	if err := store.Create(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}
