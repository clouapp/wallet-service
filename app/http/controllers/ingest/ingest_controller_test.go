package ingest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	_ "github.com/macrowallets/waas/app/adapters/ingest/alchemy"
	_ "github.com/macrowallets/waas/app/adapters/ingest/helius"
	_ "github.com/macrowallets/waas/app/adapters/ingest/quicknode"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

func TestNew_IngestController_KeepsItsDependency(t *testing.T) {
	inbound := &recordingReceiver{}
	ctrl := NewIngestController(inbound)
	if ctrl == nil {
		t.Fatal("NewIngestController returned nil")
	}
	if ctrl.inbound != inboundReceiver(inbound) {
		t.Fatal("ingest controller did not keep the inbound webhook service")
	}
}

func TestNew_IngestController_RequiresTheInboundService(t *testing.T) {
	defer func() {
		if got := recover(); got != "ingest controller: inbound webhook service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewIngestController(nil)
	t.Fatal("expected a panic")
}

func TestProvider_Signature_FailsClosedBeforeParsing(t *testing.T) {
	raw := rawWebhookBody()
	provider := &scriptedProvider{valid: true}
	store := &memorySubs{}
	recorder, continued := postIngest(t, "/v1/webhooks/ingest/alchemy/eth", raw, middleware.InboundSignatureDeps{
		Subscriptions: ingestsvc.NewSubscriptions(store),
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{"alchemy": provider}
		},
		Decrypt: func(string) (string, error) { return "opened-signing-secret", nil },
	})
	if recorder.Code != http.StatusNotFound || continued() {
		t.Fatalf("status = %d continued = %v body %s", recorder.Code, continued(), recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) || !strings.Contains(recorder.Body.String(), `"message":"webhook subscription not found"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if provider.verifyCalls != 0 || provider.parseCalls != 0 {
		t.Fatal("missing subscription reached verification")
	}
	if strings.Contains(recorder.Body.String(), "RAW-WEBHOOK-BODY-DO-NOT-LOG") {
		t.Fatalf("body leaked: %s", recorder.Body.String())
	}

	_, other := postIngest(t, "/v1/ping", []byte(`{"ok":true}`), middleware.InboundSignatureDeps{
		Subscriptions: ingestsvc.NewSubscriptions(store),
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{"alchemy": provider}
		},
	})
	if !other() || provider.verifyCalls != 0 {
		t.Fatal("a non-ingest route was treated as a webhook")
	}
}

func TestStore_RefusesAnUnverifiedBodyBeforeParsing(t *testing.T) {
	provider := &scriptedProvider{}
	sink := &recordingIngest{}
	receiver := &recordingReceiver{}
	ctrl := NewIngestController(receiver)
	ctx, recorder := newIngestContext(rawWebhookBody(), "")
	if err := ctrl.Store(ctx).Render(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"invalid_signature"`) || !strings.Contains(recorder.Body.String(), `"message":"invalid webhook signature"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if provider.parseCalls != 0 || sink.calls != 0 || receiver.calls != 0 {
		t.Fatalf("parse = %d, ingest = %d, received = %d", provider.parseCalls, sink.calls, receiver.calls)
	}
}

func TestStore_HandsATypedEventAfterTheSignature(t *testing.T) {
	const (
		bodyMarker   = "RAW-WEBHOOK-BODY-DO-NOT-LOG"
		secretMarker = "opened-signing-secret"
		sigMarker    = "sig-value-do-not-log"
	)
	raw := []byte(`[{"marker":"` + bodyMarker + `"}]`)
	provider := &scriptedProvider{
		valid:     true,
		transfers: []providers.InboundTransfer{{TxHash: "tx-typed"}},
	}
	store := &memorySubs{sub: &models.WebhookSubscription{SigningSecret: "sealed-signing-secret"}}
	sink := &recordingIngest{}
	ctx, recorder := newIngestContext(raw, sigMarker)
	middleware.ProviderSignature(middleware.InboundSignatureDeps{
		Subscriptions: ingestsvc.NewSubscriptions(store),
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{"alchemy": provider}
		},
		Decrypt: func(ciphertext string) (string, error) {
			if ciphertext != "sealed-signing-secret" {
				t.Fatalf("ciphertext = %q", ciphertext)
			}
			return secretMarker, nil
		},
	})(ctx)
	if provider.parseCalls != 0 {
		t.Fatal("middleware parsed the body")
	}
	origin := ginInstanceOf(ctx).Request
	if origin.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("content-type = %s", origin.Header.Get("Content-Type"))
	}
	// Building the Goravel request JSON-decodes application/json and would log this array.
	_ = ginpkg.NewContext(ginInstanceOf(ctx)).Request()
	rest, err := io.ReadAll(origin.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != string(raw) {
		t.Fatalf("restored body = %s", rest)
	}
	if provider.verifyCalls != 1 || string(provider.gotBody) != string(raw) || provider.gotSecret != secretMarker {
		t.Fatalf("verify calls=%d secret=%q body=%q", provider.verifyCalls, provider.gotSecret, provider.gotBody)
	}
	handlerCtx := ginpkg.NewContext(ginInstanceOf(ctx))
	ctrl := NewIngestController(ingestsvc.NewInbound(sink, func() map[string]providers.WebhookProvider {
		return map[string]providers.WebhookProvider{"alchemy": provider}
	}))
	if err := ctrl.Store(handlerCtx).Render(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if sink.calls != 1 || len(sink.event.Transfers) != 1 || sink.event.Transfers[0].TxHash != "tx-typed" || sink.event.ChainID != "eth" {
		t.Fatalf("event = %+v calls %d", sink.event, sink.calls)
	}
	if provider.parseCalls != 1 {
		t.Fatalf("parse calls = %d", provider.parseCalls)
	}
	written := recorder.Body.String()
	for _, secret := range []string{bodyMarker, secretMarker, sigMarker, "sealed-signing-secret"} {
		if strings.Contains(written, secret) {
			t.Fatalf("response leaked %q", secret)
		}
	}
}

type recordingReceiver struct {
	calls int
	err   error
}

func (r *recordingReceiver) Receive(context.Context, ingestsvc.Webhook) error {
	r.calls++
	return r.err
}

type recordingIngest struct {
	calls int
	event ingestsvc.InboundEvent
}

func (r *recordingIngest) Ingest(_ context.Context, event ingestsvc.InboundEvent) error {
	r.calls++
	r.event = event
	return nil
}

type memorySubs struct {
	sub *models.WebhookSubscription
}

func (m *memorySubs) FindByProviderAndChain(context.Context, string, string) (*models.WebhookSubscription, error) {
	if m.sub == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return m.sub, nil
}

type scriptedProvider struct {
	valid       bool
	transfers   []providers.InboundTransfer
	verifyCalls int
	parseCalls  int
	gotBody     []byte
	gotSecret   string
}

func (p *scriptedProvider) ProviderName() string { return "alchemy" }
func (p *scriptedProvider) CreateWebhook(context.Context, providers.ProviderConfig) (*providers.ProviderWebhook, error) {
	return nil, nil
}
func (p *scriptedProvider) SyncAddresses(context.Context, string, []string) error { return nil }
func (p *scriptedProvider) DeleteWebhook(context.Context, string) error           { return nil }
func (p *scriptedProvider) VerifyInbound(_ providers.Header, body []byte, secret string) (bool, error) {
	p.verifyCalls++
	p.gotBody = append([]byte(nil), body...)
	p.gotSecret = secret
	return p.valid, nil
}
func (p *scriptedProvider) ParsePayload([]byte) ([]providers.InboundTransfer, error) {
	p.parseCalls++
	return p.transfers, nil
}

func rawWebhookBody() []byte {
	return []byte(`[{"marker":"RAW-WEBHOOK-BODY-DO-NOT-LOG"}]`)
}

func postIngest(t *testing.T, path string, body []byte, deps middleware.InboundSignatureDeps) (*httptest.ResponseRecorder, func() bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	continued := false
	engine := gin.New()
	engine.POST(path, func(c *gin.Context) {
		middleware.ProviderSignature(deps)(ginpkg.NewContext(c))
	}, func(*gin.Context) { continued = true })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Alchemy-Signature", "sig-value-do-not-log")
	engine.ServeHTTP(recorder, request)
	return recorder, func() bool { return continued }
}

func newIngestContext(body []byte, signature string) (contractshttp.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	gctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/ingest/alchemy/eth", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if signature != "" {
		request.Header.Set("X-Alchemy-Signature", signature)
	}
	gctx.Request = request
	gctx.Params = gin.Params{{Key: "provider", Value: "alchemy"}, {Key: "chainID", Value: "eth"}}
	return ginpkg.NewContext(gctx), recorder
}

func ginInstanceOf(ctx contractshttp.Context) *gin.Context {
	return ctx.(interface{ Instance() *gin.Context }).Instance()
}

func TestStore_AnswersTheServiceRefusalsAsTheyAlwaysWere(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		body   string
	}{
		"unknown provider": {ingestsvc.ErrUnknownProvider, http.StatusBadRequest, `{"error":{"code":"invalid_request","message":"unknown provider"}}`},
		"invalid payload":  {ingestsvc.ErrInvalidPayload, http.StatusBadRequest, `{"error":{"code":"invalid_request","message":"invalid payload"}}`},
		"ingest failure":   {errors.New("db down"), http.StatusInternalServerError, `{"error":{"code":"internal","message":"internal error"}}` + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &scriptedProvider{valid: true}
			store := &memorySubs{sub: &models.WebhookSubscription{SigningSecret: "sealed"}}
			ctx, recorder := newIngestContext(rawWebhookBody(), "sig")
			middleware.ProviderSignature(middleware.InboundSignatureDeps{
				Subscriptions: ingestsvc.NewSubscriptions(store),
				Lookup: func() map[string]providers.WebhookProvider {
					return map[string]providers.WebhookProvider{"alchemy": provider}
				},
				Decrypt: func(string) (string, error) { return "secret", nil },
			})(ctx)
			ctrl := NewIngestController(&recordingReceiver{err: tc.err})
			if err := ctrl.Store(ginpkg.NewContext(ginInstanceOf(ctx))).Render(); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != tc.status || recorder.Body.String() != tc.body {
				t.Fatalf("status = %d body %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}
