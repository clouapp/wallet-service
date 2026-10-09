package apitoken_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/apitoken"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/withdraw"
)

const mintKey = "mint-test-signing-key-of-32-chars!"

type recordingTokens struct {
	stored *models.AccessToken
	err    error
}

func (r *recordingTokens) CreateAccessToken(_ context.Context, token *models.AccessToken) error {
	if r.err != nil {
		return r.err
	}
	copied := *token
	r.stored = &copied
	return nil
}

func newMinter(tokens *recordingTokens) *apitoken.Service {
	return apitoken.NewService(apitoken.Deps{
		Tokens:     tokens,
		Secrets:    authsvc.NewService(nil),
		SigningKey: mintKey,
	})
}

func parseMinted(t *testing.T, raw string) *apitoken.Claims {
	t.Helper()
	claims := &apitoken.Claims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return []byte(mintKey), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("the minted JWT does not verify with the signing key: %v", err)
	}
	return claims
}

func TestMint_Stores_OnlyTheDigestOfTheSecretTheJWTCarries(t *testing.T) {
	tokens := &recordingTokens{}
	accountID, creatorID := uuid.New(), uuid.New()

	minted, err := newMinter(tokens).Mint(context.Background(), apitoken.MintInput{
		AccountID: accountID,
		CreatedBy: creatorID,
		Name:      "CI token",
	})
	if err != nil {
		t.Fatal(err)
	}

	claims := parseMinted(t, minted.JWT)
	if claims.Secret == "" {
		t.Fatal("the JWT carries no secret claim")
	}
	sum := sha256.Sum256([]byte(claims.Secret))
	if tokens.stored == nil || tokens.stored.TokenHash != hex.EncodeToString(sum[:]) {
		t.Fatal("the stored token hash is not sha256 of the secret claim")
	}
	if tokens.stored.TokenHash == claims.Secret {
		t.Fatal("the secret itself was stored")
	}
	if claims.ID != tokens.stored.ID.String() || claims.AccountID != accountID.String() {
		t.Fatalf("claims name jti %s account %s, want the stored row", claims.ID, claims.AccountID)
	}
	if sub, _ := claims.GetSubject(); sub != apitoken.Subject {
		t.Fatalf("sub = %q", sub)
	}
	if claims.RequireSignature || claims.ExpiresAt != nil {
		t.Fatal("a token minted without require_signature or valid_until carries them")
	}
	if minted.Token == nil || minted.Token.ID != tokens.stored.ID {
		t.Fatal("Mint did not return the stored row")
	}
	if tokens.stored.CreatedBy == nil || *tokens.stored.CreatedBy != creatorID || tokens.stored.AccountID != accountID || tokens.stored.Name != "CI token" {
		t.Fatalf("stored row = %+v", tokens.stored)
	}
}

func TestMint_Stores_TheRestrictionsAsTheirColumns(t *testing.T) {
	tokens := &recordingTokens{}
	validUntil := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)

	minted, err := newMinter(tokens).Mint(context.Background(), apitoken.MintInput{
		AccountID:        uuid.New(),
		CreatedBy:        uuid.New(),
		Name:             "locked",
		ValidUntil:       validUntil.Format(time.RFC3339),
		RequireSignature: true,
		Permissions:      []string{"wallets.read", "webhooks.write"},
		IPCIDR:           " 192.0.2.0/24 ",
		SpendingLimit:    map[string]any{"daily_usd": "12.50"},
	})
	if err != nil {
		t.Fatal(err)
	}

	stored := tokens.stored
	if stored.Permissions != `["wallets.read","webhooks.write"]` {
		t.Fatalf("permissions = %q", stored.Permissions)
	}
	if stored.IpCidr != "192.0.2.0/24" {
		t.Fatalf("ip_cidr = %q", stored.IpCidr)
	}
	if stored.SpendingLimit != `{"daily_usd":"12.50"}` {
		t.Fatalf("spending_limit = %q", stored.SpendingLimit)
	}
	if stored.ValidUntil == nil || !stored.ValidUntil.Equal(validUntil) {
		t.Fatalf("valid_until = %v", stored.ValidUntil)
	}
	claims := parseMinted(t, minted.JWT)
	if !claims.RequireSignature {
		t.Fatal("require_signature is not on the JWT")
	}
	if claims.ExpiresAt == nil || !claims.ExpiresAt.Time.Equal(validUntil) {
		t.Fatalf("exp = %v, want valid_until", claims.ExpiresAt)
	}
}

func TestMint_Leaves_AnEmptyGrantAndABlankCapUnset(t *testing.T) {
	tokens := &recordingTokens{}

	if _, err := newMinter(tokens).Mint(context.Background(), apitoken.MintInput{
		AccountID:     uuid.New(),
		CreatedBy:     uuid.New(),
		Name:          "open",
		Permissions:   []string{},
		SpendingLimit: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}

	if tokens.stored.Permissions != "" {
		t.Fatalf("an empty grant was stored as %q, want empty text (NULL)", tokens.stored.Permissions)
	}
	if tokens.stored.SpendingLimit != "{}" {
		t.Fatalf("a blank cap was stored as %q, want {}", tokens.stored.SpendingLimit)
	}
	if tokens.stored.IpCidr != "" || tokens.stored.ValidUntil != nil {
		t.Fatalf("stored row = %+v", tokens.stored)
	}
}

func TestMint_Refuses_ANegativeCapBeforeStoring(t *testing.T) {
	tokens := &recordingTokens{}

	_, err := newMinter(tokens).Mint(context.Background(), apitoken.MintInput{
		AccountID:     uuid.New(),
		CreatedBy:     uuid.New(),
		Name:          "negative",
		SpendingLimit: map[string]any{"daily_usd": "-1"},
	})
	if !errors.Is(err, withdraw.ErrNegativeSpendingLimit) {
		t.Fatalf("err = %v, want the negative cap refusal", err)
	}
	if tokens.stored != nil {
		t.Fatal("a refused token was stored")
	}
}

func TestMint_Returns_TheStoreFailureAndNoJWT(t *testing.T) {
	failure := errors.New("access_tokens unavailable")
	tokens := &recordingTokens{err: failure}

	minted, err := newMinter(tokens).Mint(context.Background(), apitoken.MintInput{
		AccountID: uuid.New(),
		CreatedBy: uuid.New(),
		Name:      "outage",
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the store failure", err)
	}
	if minted.JWT != "" || minted.Token != nil {
		t.Fatal("a token that was not stored was returned")
	}
}
