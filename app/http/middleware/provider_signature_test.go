package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/models"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

const (
	bodyMarker   = "RAW-WEBHOOK-BODY-DO-NOT-LOG"
	secretMarker = "opened-signing-secret"
	sealedMarker = "sealed-signing-secret"
	sigMarker    = "sig-value-do-not-log"
)

func TestProvider_Signature_RejectsABadSignatureBeforeParsing(t *testing.T) {
	logs := captureLogs(t)
	provider := &signatureProvider{valid: false}
	store := &signatureSubs{sub: &models.WebhookSubscription{SigningSecret: sealedMarker}}
	recorder, next := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), signatureDeps(t, store, provider), nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	assertErrorBody(t, recorder.Body.String(), "invalid_signature", "invalid webhook signature")
	if next() {
		t.Fatal("a bad signature reached the rest of the chain")
	}
	if provider.parseCalls != 0 || provider.verifyCalls != 1 {
		t.Fatalf("verify = %d, parse = %d", provider.verifyCalls, provider.parseCalls)
	}
	assertNoSecrets(t, recorder.Body.String()+logs.String())
}

func TestProvider_Signature_DoesNotParseBeforeTheSignatureAndKeepsTheRawBody(t *testing.T) {
	logs := captureLogs(t)
	provider := &signatureProvider{valid: true}
	store := &signatureSubs{sub: &models.WebhookSubscription{SigningSecret: sealedMarker}}
	var held *gin.Context
	recorder, next := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), signatureDeps(t, store, provider), func(c *gin.Context) {
		held = c
		if c.Request.Header.Get("Content-Type") != "application/octet-stream" {
			t.Fatalf("content-type = %s", c.Request.Header.Get("Content-Type"))
		}
		// Building the Goravel request JSON-decodes application/json. The
		// array body would be logged on that path.
		_ = ginpkg.NewContext(c).Request()
		rest, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(rest) != string(jsonArrayBody()) {
			t.Fatalf("restored body = %s", rest)
		}
	})
	if !next() {
		t.Fatal("a valid signature did not continue")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("middleware wrote %s", recorder.Body.String())
	}
	if provider.parseCalls != 0 {
		t.Fatal("middleware parsed the provider payload")
	}
	if string(provider.gotBody) != string(jsonArrayBody()) || provider.gotSecret != secretMarker {
		t.Fatalf("verify saw secret %q body %q", provider.gotSecret, provider.gotBody)
	}
	ProviderSignature(signatureDeps(t, store, provider))(ginpkg.NewContext(held))
	if provider.verifyCalls != 1 || store.calls != 1 {
		t.Fatalf("second pass verify=%d lookups=%d", provider.verifyCalls, store.calls)
	}
	assertNoSecrets(t, logs.String())
}

func TestProvider_Signature_FailsClosedWhenTheSubscriptionIsMissing(t *testing.T) {
	logs := captureLogs(t)
	provider := &signatureProvider{valid: true}
	store := &signatureSubs{}
	recorder, next := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), signatureDeps(t, store, provider), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if next() || provider.verifyCalls != 0 || provider.parseCalls != 0 {
		t.Fatal("a missing subscription reached signature verification")
	}
	assertErrorBody(t, recorder.Body.String(), "not_found", "webhook subscription not found")
	assertNoSecrets(t, recorder.Body.String()+logs.String())
}

func TestProvider_Signature_LeavesOtherRoutesAlone(t *testing.T) {
	provider := &signatureProvider{}
	store := &signatureSubs{}
	_, next := runInbound(t, "/v1/ping", []byte(`{"ok":true}`), signatureDeps(t, store, provider), nil)
	if !next() || store.calls != 0 || provider.verifyCalls != 0 {
		t.Fatalf("continued=%v lookups=%d verify=%d", next(), store.calls, provider.verifyCalls)
	}
}

func TestProvider_Signature_DoesNotLogADecryptFailure(t *testing.T) {
	logs := captureLogs(t)
	provider := &signatureProvider{valid: true}
	store := &signatureSubs{sub: &models.WebhookSubscription{SigningSecret: sealedMarker}}
	deps := signatureDeps(t, store, provider)
	deps.Decrypt = func(ciphertext string) (string, error) {
		if ciphertext != sealedMarker {
			t.Fatalf("ciphertext = %q", ciphertext)
		}
		return "", errDecrypt
	}
	recorder, next := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), deps, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	assertErrorBody(t, recorder.Body.String(), "internal", "configuration error")
	if next() || provider.verifyCalls != 0 {
		t.Fatal("decrypt failure reached signature verification")
	}
	assertNoSecrets(t, recorder.Body.String()+logs.String())
}

var errDecrypt = errString("decrypt failed")

type errString string

func (e errString) Error() string { return string(e) }

func runInbound(t *testing.T, path string, body []byte, deps InboundSignatureDeps, after func(*gin.Context)) (*httptest.ResponseRecorder, func() bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	continued := false
	var held *gin.Context
	engine := gin.New()
	engine.POST(path, func(c *gin.Context) {
		held = c
		ProviderSignature(deps)(ginpkg.NewContext(c))
	}, func(c *gin.Context) {
		continued = true
		if after != nil {
			after(c)
		}
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Alchemy-Signature", sigMarker)
	engine.ServeHTTP(recorder, request)
	_ = held
	return recorder, func() bool { return continued }
}

func signatureDeps(t *testing.T, store *signatureSubs, provider *signatureProvider) InboundSignatureDeps {
	t.Helper()
	return InboundSignatureDeps{
		Subscriptions: ingestsvc.NewSubscriptions(store),
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{"alchemy": provider}
		},
		Decrypt: func(ciphertext string) (string, error) {
			if ciphertext != sealedMarker {
				t.Fatalf("ciphertext = %q", ciphertext)
			}
			return secretMarker, nil
		},
	}
}

func jsonArrayBody() []byte {
	return []byte(`[{"marker":"` + bodyMarker + `"}]`)
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func assertErrorBody(t *testing.T, body, code, message string) {
	t.Helper()
	if !strings.Contains(body, `"code":"`+code+`"`) || !strings.Contains(body, `"message":"`+message+`"`) {
		t.Fatalf("body = %s", body)
	}
}

func assertNoSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{bodyMarker, secretMarker, sealedMarker, sigMarker} {
		if strings.Contains(text, secret) {
			t.Fatalf("output leaked %q: %s", secret, text)
		}
	}
}

type signatureSubs struct {
	sub   *models.WebhookSubscription
	calls int
}

func (s *signatureSubs) FindByProviderAndChain(context.Context, string, string) (*models.WebhookSubscription, error) {
	s.calls++
	if s.sub == nil {
		return nil, models.ErrRepositoryNotFound
	}
	return s.sub, nil
}

type signatureProvider struct {
	valid       bool
	verifyCalls int
	parseCalls  int
	gotBody     []byte
	gotSecret   string
}

func (p *signatureProvider) ProviderName() string { return "alchemy" }
func (p *signatureProvider) CreateWebhook(context.Context, providers.ProviderConfig) (*providers.ProviderWebhook, error) {
	return nil, nil
}
func (p *signatureProvider) SyncAddresses(context.Context, string, []string) error { return nil }
func (p *signatureProvider) DeleteWebhook(context.Context, string) error           { return nil }
func (p *signatureProvider) VerifyInbound(headers providers.Header, body []byte, secret string) (bool, error) {
	p.verifyCalls++
	p.gotBody = append([]byte(nil), body...)
	p.gotSecret = secret
	if headers.Get("X-Alchemy-Signature") == "" {
		return false, errString("missing signature header")
	}
	return p.valid, nil
}
func (p *signatureProvider) ParsePayload([]byte) ([]providers.InboundTransfer, error) {
	p.parseCalls++
	return nil, nil
}

func TestProvider_Signature_RefusesToBuildWithoutSubscriptions(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ProviderSignature built without a subscriptions lookup")
		}
	}()
	ProviderSignature(InboundSignatureDeps{})
}
