package apitoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// ErrSign is a token that was stored but whose JWT could not be signed.
var ErrSign = errors.New("api token: sign the jwt")

// Store writes a new access token together with its token.created activity
// (account.Service.CreateAccessToken).
type Store interface {
	CreateAccessToken(ctx context.Context, token *models.AccessToken) error
}

// Secrets makes the one-time secret of a new token and the digest stored in
// its place (auth.Service).
type Secrets interface {
	GenerateAPITokenSecret() (string, error)
	HashAPITokenSecret(secret string) string
}

// Deps is everything Service needs. SigningKey is the jwt.secret the session
// JWTs are signed with, which APITokenAuth verifies the API tokens with.
type Deps struct {
	Tokens     Store
	Secrets    Secrets
	SigningKey string
}

// Service mints the dashboard's account API tokens.
type Service struct {
	tokens     Store
	secrets    Secrets
	signingKey string
}

// NewService builds the token service from Deps.
func NewService(deps Deps) *Service {
	return &Service{tokens: deps.Tokens, secrets: deps.Secrets, signingKey: deps.SigningKey}
}

// MintInput is a create-token request that passed its form request.
// ValidUntil is RFC3339 or empty; a blank IPCIDR, an empty Permissions and an
// empty SpendingLimit leave the token without that restriction.
type MintInput struct {
	AccountID        uuid.UUID
	CreatedBy        uuid.UUID
	Name             string
	ValidUntil       string
	RequireSignature bool
	Permissions      []string
	IPCIDR           string
	SpendingLimit    map[string]any
}

// Minted is the stored token and the JWT shown once.
type Minted struct {
	Token *models.AccessToken
	JWT   string
}

// Mint stores a new API token of the account and signs the JWT that carries
// its random secret. Only sha256 of the secret is stored, so the JWT cannot be
// shown again. A spending limit that is not a non-negative daily_usd is the
// withdraw sentinel and nothing is stored. A signing failure after the row was
// stored is ErrSign.
func (s *Service) Mint(ctx context.Context, in MintInput) (Minted, error) {
	permissions, err := storedPermissions(in.Permissions)
	if err != nil {
		return Minted{}, err
	}
	spendingLimit, err := withdraw.StoreSpendingLimit(in.SpendingLimit)
	if err != nil {
		return Minted{}, err
	}
	validUntil, err := parseValidUntil(in.ValidUntil)
	if err != nil {
		return Minted{}, err
	}
	secret, err := s.secrets.GenerateAPITokenSecret()
	if err != nil {
		return Minted{}, err
	}

	createdBy := in.CreatedBy
	token := &models.AccessToken{
		ID:            uuid.New(),
		AccountID:     in.AccountID,
		CreatedBy:     &createdBy,
		Name:          in.Name,
		TokenHash:     s.secrets.HashAPITokenSecret(secret),
		Permissions:   permissions,
		IpCidr:        strings.TrimSpace(in.IPCIDR),
		SpendingLimit: spendingLimit,
		ValidUntil:    validUntil,
	}
	if err := s.tokens.CreateAccessToken(ctx, token); err != nil {
		return Minted{}, err
	}

	signed, err := Sign(s.signingKey, token, in.RequireSignature, secret)
	if err != nil {
		return Minted{}, fmt.Errorf("%w: %w", ErrSign, err)
	}
	return Minted{Token: token, JWT: signed}, nil
}

// storedPermissions keeps an empty grant as empty text. The repository omits
// that value so the jsonb column stays NULL. A non-empty grant is the JSON
// array the plan stores.
func storedPermissions(permissions []string) (string, error) {
	if len(permissions) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(permissions)
	if err != nil {
		return "", fmt.Errorf("encode api token permissions: %w", err)
	}
	return string(raw), nil
}

func parseValidUntil(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("api token valid_until: %w", err)
	}
	return &parsed, nil
}
