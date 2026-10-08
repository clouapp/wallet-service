package middleware

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

const inboundWebhookPrefix = "/v1/webhooks/ingest/"

// inboundWebhook is the raw body kept for the signature check and for the
// handler's parse. The secret and the signature value are not stored.
type inboundWebhook struct {
	provider string
	chainID  string
	body     []byte
}

type inboundWebhookKey struct{}

// InboundSignatureDeps is the subscription lookup and the provider whose
// VerifyInbound implements the existing signature scheme. Subscriptions is
// required; a nil Lookup uses the built-in providers and a nil Decrypt opens
// the secret with the application key.
type InboundSignatureDeps struct {
	Subscriptions *ingestsvc.Subscriptions
	Lookup        func() map[string]providers.WebhookProvider
	Decrypt       func(ciphertext string) (string, error)
}

// ProviderSignature checks the provider signature on
// POST /v1/webhooks/ingest/{provider}/{chainID} before the body is parsed.
// Any other route continues. A failed check aborts and does not reach ingest.
func ProviderSignature(deps InboundSignatureDeps) contractshttp.Middleware {
	if deps.Subscriptions == nil {
		panic("provider signature: webhook subscriptions are required")
	}
	return func(ctx contractshttp.Context) {
		if _, _, _, ok := VerifiedInboundWebhook(ctx); ok {
			continueChain(ctx)
			return
		}
		ginCtx := ginInstance(ctx)
		providerName, chainID, ok := inboundWebhookNames(ginCtx)
		if !ok {
			continueChain(ctx)
			return
		}
		rawBody, status, message := readInboundBody(ginCtx.Request)
		if status != 0 {
			abortWithJSON(ctx, status, contractshttp.Json{"error": message})
			return
		}

		sub, status, message := inboundSubscription(ctx, deps, providerName, chainID)
		if status != 0 {
			abortWithJSON(ctx, status, contractshttp.Json{"error": message})
			return
		}
		secret, err := decryptInboundSecret(deps, sub.SigningSecret)
		if err != nil {
			slog.Error("ingest decrypt signing secret")
			abortWithJSON(ctx, http.StatusInternalServerError, contractshttp.Json{"error": "configuration error"})
			return
		}
		provider, found := deps.provider(providerName)
		if !found {
			abortWithJSON(ctx, http.StatusBadRequest, contractshttp.Json{"error": "unknown provider"})
			return
		}
		valid, verifyErr := provider.VerifyInbound(providers.Header(ginCtx.Request.Header), rawBody, secret)
		if verifyErr != nil || !valid {
			slog.Warn("ingest webhook signature rejected", "provider", providerName)
			abortWithJSON(ctx, http.StatusUnauthorized, contractshttp.Json{"error": "invalid webhook signature"})
			return
		}

		// Later middleware builds the Goravel request, which JSON-decodes
		// application/json. The signature already covered these bytes.
		ginCtx.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
		ginCtx.Request.ContentLength = int64(len(rawBody))
		ginCtx.Request.Header.Set("Content-Type", "application/octet-stream")
		ctx.WithValue(inboundWebhookKey{}, inboundWebhook{
			provider: providerName,
			chainID:  chainID,
			body:     rawBody,
		})
		continueChain(ctx)
	}
}

// VerifiedInboundWebhook returns the raw body kept for the handler after
// ProviderSignature accepts the provider signature.
func VerifiedInboundWebhook(ctx contractshttp.Context) (providerName, chainID string, body []byte, ok bool) {
	if ctx == nil {
		return "", "", nil, false
	}
	verified, ok := ctx.Value(inboundWebhookKey{}).(inboundWebhook)
	if !ok {
		return "", "", nil, false
	}
	return verified.provider, verified.chainID, verified.body, true
}

func (d InboundSignatureDeps) provider(name string) (providers.WebhookProvider, bool) {
	if d.Lookup != nil {
		return providers.Resolve(name, d.Lookup)
	}
	return providers.Resolve(name, nil)
}

func inboundWebhookNames(ginCtx *gin.Context) (providerName, chainID string, ok bool) {
	if ginCtx == nil || ginCtx.Request == nil || ginCtx.Request.URL == nil {
		return "", "", false
	}
	if ginCtx.Request.Method != http.MethodPost {
		return "", "", false
	}
	path := ginCtx.Request.URL.Path
	if !strings.HasPrefix(path, inboundWebhookPrefix) {
		return "", "", false
	}
	providerName = strings.ToLower(strings.TrimSpace(ginCtx.Param("provider")))
	chainID = strings.TrimSpace(ginCtx.Param("chainID"))
	if providerName != "" && chainID != "" {
		return providerName, chainID, true
	}
	rest := strings.TrimPrefix(path, inboundWebhookPrefix)
	providerName, chainID, found := strings.Cut(rest, "/")
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	chainID = strings.TrimSpace(chainID)
	if !found || providerName == "" || chainID == "" || strings.Contains(chainID, "/") {
		return "", "", false
	}
	return providerName, chainID, true
}

func readInboundBody(req *http.Request) ([]byte, int, string) {
	if req.Body == nil || req.Body == http.NoBody {
		return []byte{}, 0, ""
	}
	if req.ContentLength > MaxGlobalBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, "request body too large"
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, MaxGlobalBodyBytes+1))
	if err != nil {
		slog.Error("ingest read body failed")
		return nil, http.StatusBadRequest, "invalid body"
	}
	if int64(len(raw)) > MaxGlobalBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, "request body too large"
	}
	return raw, 0, ""
}

func inboundSubscription(ctx contractshttp.Context, deps InboundSignatureDeps, providerName, chainID string) (*models.WebhookSubscription, int, string) {
	sub, err := deps.Subscriptions.FindByProviderAndChain(ctx.Context(), providerName, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		sub, err = nil, nil
	}
	if err != nil {
		slog.Error("ingest subscription lookup", "provider", providerName, "chain", chainID)
		return nil, http.StatusInternalServerError, "subscription lookup failed"
	}
	if sub == nil {
		return nil, http.StatusNotFound, "webhook subscription not found"
	}
	return sub, 0, ""
}

func decryptInboundSecret(deps InboundSignatureDeps, ciphertext string) (string, error) {
	if deps.Decrypt != nil {
		return deps.Decrypt(ciphertext)
	}
	return facades.Crypt().DecryptString(ciphertext)
}
