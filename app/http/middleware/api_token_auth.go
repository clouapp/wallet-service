package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/apitoken"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/packages/activitylog"
)

// apiTokenLookup loads the access token row named by a bearer JWT and
// stamps last_used_at after authentication succeeds.
type apiTokenLookup interface {
	FindAccessToken(ctx context.Context, tokenID, accountID uuid.UUID) (*models.AccessToken, error)
	RecordAPITokenUse(ctx context.Context, tokenID, accountID uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error)
}

// APITokenAuth validates a Bearer JWT issued as an account API token.
func APITokenAuth(tokens apiTokenLookup) http.Middleware {
	if tokens == nil {
		panic("api token auth: access token lookup is required")
	}
	return func(ctx http.Context) {
		bearer := ctx.Request().Header("Authorization", "")
		if !strings.HasPrefix(bearer, "Bearer ") {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "missing bearer token").Abort()
			return
		}
		rawToken := strings.TrimPrefix(bearer, "Bearer ")

		secret := facades.Config().GetString("jwt.secret")
		claims := &apitoken.Claims{}
		parsed, err := jwt.ParseWithClaims(rawToken, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !parsed.Valid {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired api token").Abort()
			return
		}
		if sub, _ := claims.GetSubject(); sub != apitoken.Subject {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "token is not an api token").Abort()
			return
		}

		tokenID, err := uuid.Parse(claims.ID)
		if err != nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid token id").Abort()
			return
		}

		accountID, err := uuid.Parse(claims.AccountID)
		if err != nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid account id in token").Abort()
			return
		}

		tokenPtr, err := tokens.FindAccessToken(ctx.Context(), tokenID, accountID)
		if err != nil || tokenPtr == nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "token not found or revoked").Abort()
			return
		}
		token := *tokenPtr

		if token.RevokedAt != nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "token not found or revoked").Abort()
			return
		}

		if token.ValidUntil != nil && token.ValidUntil.Before(time.Now()) {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "token expired").Abort()
			return
		}
		if !authsvc.APITokenHashAccepts(claims.Secret, token.TokenHash) {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired api token").Abort()
			return
		}

		sig := ctx.Request().Header("X-Signature", "")
		if claims.RequireSignature && sig == "" {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeInvalidSignature, "missing request signature").Abort()
			return
		}
		if sig != "" {
			bodyBytes, _ := io.ReadAll(ctx.Request().Origin().Body)
			ctx.Request().Origin().Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			mac := hmac.New(sha256.New, []byte(rawToken))
			mac.Write(bodyBytes)
			expected := hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(expected)) {
				_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeInvalidSignature, "invalid request signature").Abort()
				return
			}
		}

		account, err := tokens.FindByID(ctx.Context(), accountID)
		if err != nil || account == nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "token not found or revoked").Abort()
			return
		}
		if !abortUnlessAccountAllows(ctx, account) {
			return
		}

		// S3.4.6 enforces ip_cidr against ClientIP. It does not name a code,
		// so a miss is the 403 forbidden the error contract uses for an
		// authenticated caller who is not permitted. A blank allowlist is
		// not a miss.
		if !policies.APITokenIPAllows(token.IpCidr, ClientIP(ctx)) {
			_ = responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, responses.CodeForbidden).Abort()
			return
		}

		actor := activitylog.WithCauser(ctx.Context(), activitylog.Causer{
			Type:  activitylog.CauserAPITokens,
			ID:    token.ID.String(),
			Label: token.Name,
		})
		actor = activitylog.WithScope(actor, "account:"+accountID.String())
		ctx.WithContext(actor)
		if err := tokens.RecordAPITokenUse(ctx.Context(), token.ID, accountID); err != nil {
			facades.Log().Errorf("api token: last_used_at was not recorded for %s", token.ID)
		}

		ctx.WithValue(requestctx.KeyAccountID, accountID)
		ctx.WithValue(requestctx.KeyAPIToken, &token)
		ctx.Request().Next()
	}
}

// MintAPIToken signs a JWT for an access token row that has no secret claim,
// with the session signing key. Test fixtures mint with it; a dashboard token
// is minted by apitoken.Service.Mint.
//
// Set requireSignature=true for a token whose callers must always sign, so
// APITokenAuth rejects a request that has no X-Signature header. A request that
// does send one is verified either way.
func MintAPIToken(token *models.AccessToken, requireSignature bool) (string, error) {
	return apitoken.Sign(facades.Config().GetString("jwt.secret"), token, requireSignature, "")
}

// MintAPITokenWithSecret signs a JWT that carries the one-time secret claim.
// The database stores only sha256 of that secret.
func MintAPITokenWithSecret(token *models.AccessToken, requireSignature bool, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("api token secret is required")
	}
	return apitoken.Sign(facades.Config().GetString("jwt.secret"), token, requireSignature, secret)
}
