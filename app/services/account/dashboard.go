package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// UpdateAccount applies a name and/or view_all_wallets change on the account
// the caller already loaded. A blank name is left as stored. Each column is
// written on its own, matching the previous handler: a later failure leaves
// the earlier column committed and the in-memory account updated only for
// the writes that succeeded.
func (s *Service) UpdateAccount(ctx context.Context, account *models.Account, name string, viewAllWallets *bool) error {
	if ctx == nil {
		return fmt.Errorf("update account: context is required")
	}
	if account == nil {
		return fmt.Errorf("update account: account is required")
	}
	if err := s.requireAccounts(); err != nil {
		return err
	}
	if name != "" {
		if err := s.accounts.SetName(ctx, account.ID, name); err != nil {
			return err
		}
		account.Name = name
	}
	if viewAllWallets != nil {
		if err := s.accounts.SetViewAllWallets(ctx, account.ID, *viewAllWallets); err != nil {
			return err
		}
		account.ViewAllWallets = *viewAllWallets
	}
	return nil
}

// SetStatus sets accounts.status and the same field on the loaded account.
func (s *Service) SetStatus(ctx context.Context, account *models.Account, status string) error {
	if ctx == nil {
		return fmt.Errorf("set account status: context is required")
	}
	if account == nil {
		return fmt.Errorf("set account status: account is required")
	}
	if status == "" {
		return fmt.Errorf("set account status: status is required")
	}
	if err := s.requireAccounts(); err != nil {
		return err
	}
	if err := s.accounts.SetStatus(ctx, account.ID, status); err != nil {
		return err
	}
	account.Status = status
	return nil
}

// ListMembers pages the account's active memberships.
func (s *Service) ListMembers(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list members: context is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list members: account id is required")
	}
	if s.memberships == nil {
		return nil, 0, fmt.Errorf("account service: memberships repository is required")
	}
	return s.memberships.PaginateByAccountID(ctx, accountID, limit, offset)
}

// FindOrCreateInvitedUser returns the user for email. A missing row, or a
// lookup error, inserts a user with status invited. invited is true only when
// this call inserted the row. The caller sends the invite mail after a
// successful insert; a mail failure must not undo the user.
func (s *Service) FindOrCreateInvitedUser(ctx context.Context, email string) (user *models.User, invited bool, err error) {
	if ctx == nil {
		return nil, false, fmt.Errorf("invite user: context is required")
	}
	if email == "" {
		return nil, false, fmt.Errorf("invite user: email is required")
	}
	if s.users == nil {
		return nil, false, fmt.Errorf("account service: users repository is required")
	}
	found, findErr := s.users.FindByEmail(ctx, email)
	if findErr == nil && found != nil {
		return found, false, nil
	}
	created := &models.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: "",
		Status:       "invited",
	}
	if err := s.users.Create(ctx, created); err != nil {
		return nil, false, err
	}
	return created, true, nil
}

// FindByID returns one account. The error is the store's error.
func (s *Service) FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find account: context is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, err
	}
	return s.accounts.FindByID(ctx, id)
}

// ListForMember pages the accounts the user belongs to. Empty search and
// environment do not filter, matching the repository.
func (s *Service) ListForMember(ctx context.Context, userID uuid.UUID, search, environment string, limit, offset int) ([]models.Account, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list member accounts: context is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, 0, err
	}
	return s.accounts.PaginateByMember(ctx, userID, search, environment, limit, offset)
}

// RolesForUserAccounts returns the caller's role on each account.
func (s *Service) RolesForUserAccounts(ctx context.Context, userID uuid.UUID, accountIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list account roles: context is required")
	}
	if s.memberships == nil {
		return nil, fmt.Errorf("account service: memberships repository is required")
	}
	return s.memberships.RolesForUserAccounts(ctx, userID, accountIDs)
}

// InsertAccount inserts an account row the caller already filled in.
func (s *Service) InsertAccount(ctx context.Context, account *models.Account) error {
	if ctx == nil {
		return fmt.Errorf("insert account: context is required")
	}
	if account == nil {
		return fmt.Errorf("insert account: account is required")
	}
	if err := s.requireAccounts(); err != nil {
		return err
	}
	return s.accounts.Create(ctx, account)
}

// LinkAccount sets accounts.linked_account_id.
func (s *Service) LinkAccount(ctx context.Context, id, linkedID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("link account: context is required")
	}
	if err := s.requireAccounts(); err != nil {
		return err
	}
	return s.accounts.SetLinkedAccountID(ctx, id, linkedID)
}

// InsertMembership inserts a membership the caller already filled in.
func (s *Service) InsertMembership(ctx context.Context, membership *models.AccountUser) error {
	if ctx == nil {
		return fmt.Errorf("insert membership: context is required")
	}
	if membership == nil {
		return fmt.Errorf("insert membership: membership is required")
	}
	if s.memberships == nil {
		return fmt.Errorf("account service: memberships repository is required")
	}
	return s.memberships.Create(ctx, membership)
}

// ListMemberships returns the caller's active memberships.
func (s *Service) ListMemberships(ctx context.Context, userID uuid.UUID) ([]models.AccountUser, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list memberships: context is required")
	}
	if s.memberships == nil {
		return nil, fmt.Errorf("account service: memberships repository is required")
	}
	return s.memberships.FindByUserID(ctx, userID)
}

// FindMember returns the active membership. The error is the store's error,
// including not-found, so the caller can log it and still answer.
func (s *Service) FindMember(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find member: context is required")
	}
	if s.memberships == nil {
		return nil, fmt.Errorf("account service: memberships repository is required")
	}
	return s.memberships.FindByAccountAndUser(ctx, accountID, userID)
}

// ListAccessTokens pages the account's API tokens.
func (s *Service) ListAccessTokens(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccessToken, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list access tokens: context is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list access tokens: account id is required")
	}
	if err := s.requireTokens(); err != nil {
		return nil, 0, err
	}
	return s.tokens.PaginateByAccountID(ctx, accountID, limit, offset)
}

// CreateAccessToken inserts a token the caller has already hashed.
func (s *Service) CreateAccessToken(ctx context.Context, token *models.AccessToken) error {
	if ctx == nil {
		return fmt.Errorf("create access token: context is required")
	}
	if token == nil {
		return fmt.Errorf("create access token: token is required")
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	return s.tokens.Create(ctx, token)
}

// FindAccessToken returns the token row for this account. The error is the store's error.
func (s *Service) FindAccessToken(ctx context.Context, tokenID, accountID uuid.UUID) (*models.AccessToken, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find access token: context is required")
	}
	if err := s.requireTokens(); err != nil {
		return nil, err
	}
	return s.tokens.FindByIDAndAccount(ctx, tokenID, accountID)
}

// RevokeAccessToken deletes one token that belongs to the account.
// A missing token is ErrAccessTokenNotFound.
func (s *Service) RevokeAccessToken(ctx context.Context, accountID, tokenID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("revoke access token: context is required")
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	token, err := s.tokens.FindByIDAndAccount(ctx, tokenID, accountID)
	if err != nil || token == nil {
		return ErrAccessTokenNotFound
	}
	return s.tokens.Delete(ctx, token)
}

func (s *Service) requireAccounts() error {
	if s.accounts == nil {
		return fmt.Errorf("account service: accounts repository is required")
	}
	return nil
}
