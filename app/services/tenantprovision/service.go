package tenantprovision

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/apitoken"
)

var ErrConflict = errors.New("tenant provisioning identity conflicts with an existing account or token")

type Accounts interface {
	FindByID(context.Context, uuid.UUID) (*models.Account, error)
	Create(context.Context, *models.Account) error
}

type Tokens interface {
	FindByIDAndAccount(context.Context, uuid.UUID, uuid.UUID) (*models.AccessToken, error)
	Create(context.Context, *models.AccessToken) error
}

type Service struct {
	Accounts     Accounts
	Tokens       Tokens
	SigningKey   string
	ProvisionKey string
}

type Result struct {
	AccountID uuid.UUID
	APIToken  string
	Created   bool
}

// Provision uses stable account and token IDs so a Lambda retry resumes after
// either database write. The API token secret is derived from the dedicated
// provisioning key and is never stored in plaintext.
func (s Service) Provision(ctx context.Context, externalID, name, environment string) (Result, error) {
	if s.Accounts == nil || s.Tokens == nil || len(s.ProvisionKey) < 32 || s.SigningKey == "" {
		return Result{}, fmt.Errorf("tenant provisioning is not configured")
	}
	if externalID == "" || len(externalID) > 128 || strings.TrimSpace(name) == "" || len(name) > 255 {
		return Result{}, fmt.Errorf("invalid tenant identity")
	}
	if environment != models.EnvironmentTest && environment != models.EnvironmentProd {
		return Result{}, fmt.Errorf("invalid account environment")
	}

	accountID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("macro-wallets/tenant/"+externalID))
	tokenID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("macro-wallets/tenant-token/"+externalID))
	result := Result{AccountID: accountID}
	account, err := s.Accounts.FindByID(ctx, accountID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return Result{}, err
	}
	if account == nil {
		candidate := &models.Account{ID: accountID, Name: name, Status: models.StatusActive, Environment: environment}
		if err := s.Accounts.Create(ctx, candidate); err != nil {
			// Another retry may have created it concurrently. Read it back before
			// deciding whether the write actually failed.
			account, err = s.Accounts.FindByID(ctx, accountID)
			if err != nil {
				return Result{}, err
			}
		} else {
			account = candidate
			result.Created = true
		}
	}
	if account == nil || account.Environment != environment || account.Status != models.StatusActive {
		return Result{}, ErrConflict
	}

	mac := hmac.New(sha256.New, []byte(s.ProvisionKey))
	_, _ = mac.Write([]byte("macro-wallets/tenant-api-token/" + externalID))
	secret := hex.EncodeToString(mac.Sum(nil))
	hash := sha256.Sum256([]byte(secret))
	tokenHash := hex.EncodeToString(hash[:])
	token, err := s.Tokens.FindByIDAndAccount(ctx, tokenID, accountID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return Result{}, err
	}
	if token == nil {
		candidate := &models.AccessToken{
			ID: tokenID, AccountID: accountID, Name: "Whitelabel tenant integration",
			TokenHash: tokenHash, Permissions: models.AllAPIPermissionGrants(),
			SpendingLimit: models.NoSpendingLimit,
		}
		if err := s.Tokens.Create(ctx, candidate); err != nil {
			token, err = s.Tokens.FindByIDAndAccount(ctx, tokenID, accountID)
			if err != nil {
				return Result{}, err
			}
		} else {
			token = candidate
		}
	}
	if token == nil || token.TokenHash != tokenHash || token.RevokedAt != nil {
		return Result{}, ErrConflict
	}
	result.APIToken, err = apitoken.Sign(s.SigningKey, token, false, secret)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
