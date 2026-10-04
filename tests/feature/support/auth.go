// Package support provides shared helpers for controller integration tests.
//
// These helpers centralise the three patterns that every external /api/v1
// controller test needs to repeat:
//
//  1. Seeding an account + access_token row.
//  2. Minting a Bearer JWT via middleware.MintAPIToken.
//  3. Building an HMAC signer keyed by the raw JWT when a token requires it.
//
// They live under tests/ so production packages do not compile them. Keep the
// helpers dependency-free of specific controller packages so any *_test.go
// under app/http/controllers can import them.
package support

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
)

// SignFunc returns the hex-encoded HMAC-SHA256 of `body`, keyed by the raw JWT
// that the signer was built for. The return value matches the X-Signature
// header format enforced by middleware.APITokenAuth for tokens minted with
// `require_signature = true`.
type SignFunc func(body []byte) string

// SetupAPIAuth seeds an Account + access_tokens row and mints a Bearer JWT for
// the external /api/v1 scheme. When requireSignature is true, the returned
// SignFunc hashes request bodies with HMAC-SHA256 keyed by the raw JWT — the
// scheme APITokenAuth enforces when the token's `require_signature` claim is
// set. The SignFunc is nil when requireSignature is false.
//
// The access_tokens table carries NOT NULL columns (token_hash, spending_limit)
// that the ORM model doesn't expose, so the row is inserted via raw SQL — the
// same pattern used in the middleware-level tests.
//
// Requires an active Goravel ORM + a migrated `accounts` and `access_tokens`
// schema. Call tests/testutil.SeededTestDB(t) or mocks.TestDB(t) first.
func SetupAPIAuth(t *testing.T, requireSignature bool) (accountID uuid.UUID, bearerJWT string, sign SignFunc) {
	t.Helper()

	accountID = uuid.New()
	shortID := accountID.String()[:8]
	if err := facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "test-account-" + shortID,
		Status:      "active",
		Environment: "prod",
	}); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	tokenID := uuid.New()
	tokenName := "test-token-" + shortID
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, tokenName, "test-hash-"+shortID, models.AllAPIPermissionGrants(), "{}",
	); err != nil {
		t.Fatalf("insert access_token: %v", err)
	}

	jwtStr, err := middleware.MintAPIToken(&models.AccessToken{
		ID:        tokenID,
		AccountID: accountID,
		Name:      tokenName,
	}, requireSignature)
	if err != nil {
		t.Fatalf("mint api token: %v", err)
	}

	if requireSignature {
		key := []byte(jwtStr)
		sign = func(body []byte) string {
			mac := hmac.New(sha256.New, key)
			mac.Write(body)
			return hex.EncodeToString(mac.Sum(nil))
		}
	}

	return accountID, jwtStr, sign
}

// SetupSessionAuth is a stub reserved for the dashboard (session) auth path.
//
// Session JWTs are minted by the live auth controller (facades.Auth().Login)
// rather than by a pure helper, so reproducing the mint in-test would require
// either (a) extracting a public mint helper from app/http/controllers/auth_controller.go
// or (b) calling the login endpoint from the test. Neither change belongs in
// T1 — T2/T3 will either route-swap the migrated tests to the external API
// (/api/v1/*) or extract the mint helper explicitly.
//
// Tests calling this helper today are skipped so we don't silently pass a
// session-auth flow that isn't exercised.
func SetupSessionAuth(t *testing.T) (accountID, userID uuid.UUID, bearerJWT string) {
	t.Helper()
	t.Skip("SetupSessionAuth not yet implemented — T2/T3 should use the external /api/v1 routes via SetupAPIAuth, or extract a public session-mint helper")
	return uuid.Nil, uuid.Nil, ""
}
