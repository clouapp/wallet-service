package apitoken

import (
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/macrowallets/waas/app/models"
)

// Subject is the sub claim of every account API token. APITokenAuth refuses a
// JWT that carries another subject.
const Subject = "api_token"

// Claims are the JWT claims embedded in account API tokens.
//
// RequireSignature, when true, forces APITokenAuth to reject requests that
// do not carry an X-Signature header. It is set at mint time from the
// create-token request's require_signature field, which defaults to false, and
// the dashboard does not send it today, so tokens minted from it do not require
// a signature. A signature that is present is verified whether or not this is
// set. The scheme and its proposed v2 are in docs/api-request-signing.md.
type Claims struct {
	AccountID        string `json:"account_id"`
	RequireSignature bool   `json:"sig,omitempty"`
	// Secret is the random 32-byte secret, hex-encoded, shown once inside the
	// minted JWT. Tokens stored before that claim omit it.
	Secret string `json:"secret,omitempty"`
	jwt.RegisteredClaims
}

// Sign signs the HS256 JWT of an access token row with key: jti is the row,
// sub is Subject, iat is now and exp is the row's valid_until when it has one.
// An empty secret leaves the secret claim out, as on the tokens minted before
// it existed.
func Sign(key string, token *models.AccessToken, requireSignature bool, secret string) (string, error) {
	claims := Claims{
		AccountID:        token.AccountID.String(),
		RequireSignature: requireSignature,
		Secret:           secret,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:       token.ID.String(),
			Subject:  Subject,
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	if token.ValidUntil != nil {
		claims.ExpiresAt = jwt.NewNumericDate(*token.ValidUntil)
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
}
