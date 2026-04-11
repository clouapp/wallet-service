package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
)

// APITokenClaims are the JWT claims embedded in account API tokens.
type APITokenClaims struct {
	AccountID string `json:"account_id"`
	jwt.RegisteredClaims
}

// APITokenAuth validates a Bearer JWT issued as an account API token.
func APITokenAuth() http.Middleware {
	return func(ctx http.Context) {
		bearer := ctx.Request().Header("Authorization", "")
		if !strings.HasPrefix(bearer, "Bearer ") {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "missing bearer token"})
			return
		}
		rawToken := strings.TrimPrefix(bearer, "Bearer ")

		secret := facades.Config().GetString("jwt.secret")
		claims := &APITokenClaims{}
		parsed, err := jwt.ParseWithClaims(rawToken, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !parsed.Valid {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired api token"})
			return
		}
		if sub, _ := claims.GetSubject(); sub != "api_token" {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "token is not an api token"})
			return
		}

		tokenID, err := uuid.Parse(claims.ID)
		if err != nil {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token id"})
			return
		}

		accountID, err := uuid.Parse(claims.AccountID)
		if err != nil {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid account id in token"})
			return
		}

		tokenPtr, err := container.Get().AccessTokenRepo.FindByIDAndAccount(tokenID, accountID)
		if err != nil || tokenPtr == nil {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "token not found or revoked"})
			return
		}
		token := *tokenPtr

		if token.ValidUntil != nil && token.ValidUntil.Before(time.Now()) {
			ctx.Request().AbortWithStatus(http.StatusUnauthorized)
			ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "token expired"})
			return
		}

		if sig := ctx.Request().Header("X-Signature", ""); sig != "" {
			bodyBytes, _ := io.ReadAll(ctx.Request().Origin().Body)
			mac := hmac.New(sha256.New, []byte(rawToken))
			mac.Write(bodyBytes)
			expected := hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(expected)) {
				ctx.Request().AbortWithStatus(http.StatusUnauthorized)
				ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid request signature"})
				return
			}
		}

		ctx.WithValue("account_id", accountID)
		ctx.WithValue("api_token", &token)
		ctx.Request().Next()
	}
}

// MintAPIToken creates a signed JWT for the given AccessToken record.
func MintAPIToken(token *models.AccessToken) (string, error) {
	secret := facades.Config().GetString("jwt.secret")
	claims := APITokenClaims{
		AccountID: token.AccountID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:       token.ID.String(),
			Subject:  "api_token",
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	if token.ValidUntil != nil {
		claims.ExpiresAt = jwt.NewNumericDate(*token.ValidUntil)
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
