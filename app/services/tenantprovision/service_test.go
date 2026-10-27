package tenantprovision_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/apitoken"
	"github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/tenantprovision"
)

type accountStore struct {
	rows   map[uuid.UUID]*models.Account
	writes int
}

func (s *accountStore) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	if row := s.rows[id]; row != nil {
		return row, nil
	}
	return nil, models.ErrRepositoryNotFound
}
func (s *accountStore) Create(_ context.Context, row *models.Account) error {
	if s.rows == nil {
		s.rows = map[uuid.UUID]*models.Account{}
	}
	s.rows[row.ID] = row
	s.writes++
	return nil
}

type tokenStore struct {
	rows   map[uuid.UUID]*models.AccessToken
	writes int
}

func (s *tokenStore) FindByIDAndAccount(_ context.Context, id, accountID uuid.UUID) (*models.AccessToken, error) {
	if row := s.rows[id]; row != nil && row.AccountID == accountID {
		return row, nil
	}
	return nil, models.ErrRepositoryNotFound
}
func (s *tokenStore) Create(_ context.Context, row *models.AccessToken) error {
	if s.rows == nil {
		s.rows = map[uuid.UUID]*models.AccessToken{}
	}
	s.rows[row.ID] = row
	s.writes++
	return nil
}

func TestProvisionRetryReusesTheAccountAndToken(t *testing.T) {
	accounts := &accountStore{}
	tokens := &tokenStore{}
	svc := tenantprovision.Service{Accounts: accounts, Tokens: tokens, SigningKey: "jwt-signing-key", ProvisionKey: "separate-provisioning-key-at-least-32-characters"}
	first, err := svc.Provision(context.Background(), "whitelabel.new:103", "Previsions Video QA", models.EnvironmentTest)
	if err != nil || !first.Created || first.APIToken == "" {
		t.Fatalf("first provision: %+v, %v", first, err)
	}
	second, err := svc.Provision(context.Background(), "whitelabel.new:103", "Previsions Video QA", models.EnvironmentTest)
	if err != nil || second.Created || second.AccountID != first.AccountID || second.APIToken == "" {
		t.Fatalf("retry: %+v, %v", second, err)
	}
	if accounts.writes != 1 || tokens.writes != 1 {
		t.Fatalf("duplicate writes: accounts=%d tokens=%d", accounts.writes, tokens.writes)
	}
	other, err := svc.Provision(context.Background(), "whitelabel.new:104", "Other tenant", models.EnvironmentTest)
	if err != nil || other.AccountID == first.AccountID {
		t.Fatalf("tenant isolation: %+v, %v", other, err)
	}
	if token := tokens.rows[uuid.NewSHA1(uuid.NameSpaceURL, []byte("macro-wallets/tenant-token/whitelabel.new:103"))]; token == nil || token.TokenHash == first.APIToken {
		t.Fatal("the raw API token was stored")
	}
	claims := &apitoken.Claims{}
	parsed, err := jwt.ParseWithClaims(first.APIToken, claims, func(*jwt.Token) (any, error) { return []byte(svc.SigningKey), nil })
	if err != nil || !parsed.Valid || claims.AccountID != first.AccountID.String() {
		t.Fatalf("issued token is not valid for the provisioned account: %v", err)
	}
	stored := tokens.rows[uuid.NewSHA1(uuid.NameSpaceURL, []byte("macro-wallets/tenant-token/whitelabel.new:103"))]
	if !auth.APITokenHashAccepts(claims.Secret, stored.TokenHash) {
		t.Fatal("issued token does not match the stored digest")
	}
}

func TestProvisionDoesNotReuseAnAccountInAnotherEnvironment(t *testing.T) {
	accounts := &accountStore{}
	tokens := &tokenStore{}
	svc := tenantprovision.Service{Accounts: accounts, Tokens: tokens, SigningKey: "jwt-signing-key", ProvisionKey: "separate-provisioning-key-at-least-32-characters"}
	_, err := svc.Provision(context.Background(), "whitelabel.new:103", "QA", models.EnvironmentTest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Provision(context.Background(), "whitelabel.new:103", "QA", models.EnvironmentProd)
	if !errors.Is(err, tenantprovision.ErrConflict) {
		t.Fatalf("environment conflict: %v", err)
	}
}
