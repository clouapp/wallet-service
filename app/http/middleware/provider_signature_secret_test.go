package middleware

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/macrowallets/waas/app/models"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

// prefixCipher stands in for the application key: it tags what it encrypts and opens
// only what it tagged.
type prefixCipher struct{}

func (prefixCipher) EncryptString(value string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte("key:" + value)), nil
}

func (prefixCipher) DecryptString(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || !strings.HasPrefix(string(raw), "key:") {
		return "", errors.New("not encrypted with this key")
	}
	return strings.TrimPrefix(string(raw), "key:"), nil
}

// sealedDeps is the production opener (ingest.SigningSecrets) over a row whose
// signing secret is stored as stored.
func sealedDeps(stored string, provider *signatureProvider) InboundSignatureDeps {
	return InboundSignatureDeps{
		Subscriptions: ingestsvc.NewSubscriptions(&signatureSubs{sub: &models.WebhookSubscription{SigningSecret: stored}}),
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{"alchemy": provider}
		},
		Decrypt: ingestsvc.NewSigningSecrets(prefixCipher{}).Open,
	}
}

func TestProvider_Signature_VerifiesWithTheSecretIngestSubscribeSealed(t *testing.T) {
	stored, err := ingestsvc.NewSigningSecrets(prefixCipher{}).Seal(secretMarker)
	if err != nil {
		t.Fatal(err)
	}
	provider := &signatureProvider{valid: true}

	rec, continued := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), sealedDeps(stored, provider), func(*gin.Context) {})

	if rec.Code != http.StatusOK || !continued() {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if provider.gotSecret != secretMarker {
		t.Fatal("the provider did not verify with the opened signing secret")
	}
}

func TestProvider_Signature_RefusesARowWhoseSecretIsNotSealed(t *testing.T) {
	provider := &signatureProvider{valid: true}

	rec, continued := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), sealedDeps(secretMarker, provider), func(*gin.Context) {})

	if rec.Code != http.StatusInternalServerError || continued() || provider.verifyCalls != 0 {
		t.Fatalf("a plaintext secret was used: status %d verify calls %d", rec.Code, provider.verifyCalls)
	}
}

func TestProvider_Signature_RefusesEveryCallbackWithoutAnOpener(t *testing.T) {
	provider := &signatureProvider{valid: true}
	deps := sealedDeps("enc:v1:x", provider)
	deps.Decrypt = nil

	rec, continued := runInbound(t, "/v1/webhooks/ingest/alchemy/eth", jsonArrayBody(), deps, func(*gin.Context) {})

	if rec.Code != http.StatusInternalServerError || continued() || provider.verifyCalls != 0 {
		t.Fatalf("status %d verify calls %d", rec.Code, provider.verifyCalls)
	}
}
