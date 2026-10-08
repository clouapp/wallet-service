package account

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

const testOrganizationSuffix = " [test]"

// OnboardInput is the register write. PasswordHash is already hashed.
// The plaintext password is not part of this input.
type OnboardInput struct {
	Email            string
	PasswordHash     string
	FullName         string
	OrganizationName string
}

// WelcomeDispatch runs after the register rows commit. It receives the user
// id and nothing else: no password, hash, token, or address.
type WelcomeDispatch func(userID uuid.UUID) error

// Onboard inserts the user, the production account, the test account, both
// owner memberships, and the default account in one transaction. Welcome
// runs only after that transaction commits. A failure leaves none of those
// rows. This does not grant a platform admin.
func (s *Service) Onboard(ctx context.Context, input OnboardInput, welcome WelcomeDispatch) (*models.User, error) {
	if s == nil {
		return nil, fmt.Errorf("onboard: account service is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("onboard: context is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, err
	}
	if s.users == nil {
		return nil, fmt.Errorf("onboard: users repository is required")
	}
	if s.memberships == nil {
		return nil, fmt.Errorf("onboard: memberships repository is required")
	}

	email := strings.TrimSpace(input.Email)
	organization := strings.TrimSpace(input.OrganizationName)
	if email == "" {
		return nil, fmt.Errorf("onboard: email is required")
	}
	if input.PasswordHash == "" {
		return nil, fmt.Errorf("onboard: password hash is required")
	}
	if organization == "" {
		return nil, fmt.Errorf("onboard: organization name is required")
	}

	user := &models.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: input.PasswordHash,
		FullName:     strings.TrimSpace(input.FullName),
		Status:       models.StatusActive,
	}
	prodID := uuid.New()
	testID := uuid.New()

	err := s.memberships.Within(ctx, func(txCtx context.Context) error {
		if err := s.users.Create(txCtx, user); err != nil {
			return err
		}
		prod := &models.Account{
			ID:          prodID,
			Name:        organization,
			Status:      models.StatusActive,
			Environment: models.EnvironmentProd,
		}
		testAccount := &models.Account{
			ID:              testID,
			Name:            organization + testOrganizationSuffix,
			Status:          models.StatusActive,
			Environment:     models.EnvironmentTest,
			LinkedAccountID: &prodID,
		}
		if err := s.accounts.Create(txCtx, prod); err != nil {
			return err
		}
		if err := s.accounts.Create(txCtx, testAccount); err != nil {
			return err
		}
		if err := s.accounts.SetLinkedAccountID(txCtx, prodID, testID); err != nil {
			return err
		}
		if err := s.memberships.Create(txCtx, ownerMembership(prodID, user.ID)); err != nil {
			return err
		}
		if err := s.memberships.Create(txCtx, ownerMembership(testID, user.ID)); err != nil {
			return err
		}
		if err := s.users.UpdateDefaultAccountID(txCtx, user.ID, &prodID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	user.DefaultAccountID = &prodID
	if welcome != nil {
		if err := welcome(user.ID); err != nil {
			return user, err
		}
	}
	return user, nil
}

func ownerMembership(accountID, userID uuid.UUID) *models.AccountUser {
	return &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      policies.AccountOwnerRole(),
		Status:    models.MembershipStatusActive,
	}
}
