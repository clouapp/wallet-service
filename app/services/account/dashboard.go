package account

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
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

// FindUserByEmail returns the user, or the store's not-found error.
func (s *Service) FindUserByEmail(ctx context.Context, email string) (*models.User, error) {
	if s.users == nil {
		return nil, fmt.Errorf("account service: users repository is required")
	}
	return s.users.FindByEmail(ctx, email)
}

// FindUserByID returns the user, or the store's not-found error.
func (s *Service) FindUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	if s.users == nil {
		return nil, fmt.Errorf("account service: users repository is required")
	}
	return s.users.FindByID(ctx, id)
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

// MemberAccount is an account with the listing user's role on it.
type MemberAccount struct {
	Account models.Account
	Role    string
}

// ListForMemberWithRoles is ListForMember with the user's stored role on each
// listed account. A listed account with no active role is a broken membership
// and fails the read instead of being served without one.
func (s *Service) ListForMemberWithRoles(ctx context.Context, userID uuid.UUID, search, environment string, limit, offset int) ([]MemberAccount, int64, error) {
	accounts, total, err := s.ListForMember(ctx, userID, search, environment, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := make([]MemberAccount, 0, len(accounts))
	if len(accounts) == 0 {
		return items, total, nil
	}

	accountIDs := make([]uuid.UUID, len(accounts))
	for i, account := range accounts {
		accountIDs[i] = account.ID
	}
	roles, err := s.RolesForUserAccounts(ctx, userID, accountIDs)
	if err != nil {
		return nil, 0, err
	}
	for _, account := range accounts {
		role, ok := roles[account.ID]
		if !ok || strings.TrimSpace(role) == "" {
			return nil, 0, fmt.Errorf("account %s has no role for user %s", account.ID, userID)
		}
		items = append(items, MemberAccount{Account: account, Role: role})
	}
	return items, total, nil
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

// CreateAccessToken inserts a token the caller has already hashed and writes
// token.created in the same transaction. The activity row keeps the name and
// catalog permissions. It does not keep the secret, the hash, or the spending limit.
func (s *Service) CreateAccessToken(ctx context.Context, token *models.AccessToken) error {
	if ctx == nil {
		return fmt.Errorf("create access token: context is required")
	}
	if token == nil || token.ID == uuid.Nil || token.AccountID == uuid.Nil {
		return fmt.Errorf("create access token: token is required")
	}
	if token.CreatedBy == nil || *token.CreatedBy == uuid.Nil {
		return fmt.Errorf("create access token: actor is required")
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	if err := s.requireActivity(); err != nil {
		return err
	}
	meta, err := activitylog.TokenCreated(token.Name, token.Permissions)
	if err != nil {
		return err
	}
	accountID := token.AccountID
	actorID := *token.CreatedBy
	tokenID := token.ID
	return s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.tokens.Create(ctx, token); err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &accountID,
			ActorUserID: actorID,
			Action:      activitylog.ActionTokenCreated,
			TargetType:  activitylog.TargetAccessToken,
			TargetID:    tokenID.String(),
			Metadata:    meta,
		})
	})
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

// RecordAPITokenUse stamps last_used_at after authentication succeeded.
// The caller keeps the response it was already going to write.
func (s *Service) RecordAPITokenUse(ctx context.Context, tokenID, accountID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("record access token use: context is required")
	}
	if tokenID == uuid.Nil || accountID == uuid.Nil {
		return fmt.Errorf("record access token use: token id and account id are required")
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	return s.tokens.RecordUse(ctx, tokenID, accountID)
}

// RevokeAccessToken soft-revokes one token that belongs to the account and
// writes token.revoked in the same transaction. The row stays. A missing
// token is ErrAccessTokenNotFound. A token that is already revoked stays
// revoked at the original time and does not gain a second activity row.
func (s *Service) RevokeAccessToken(ctx context.Context, accountID, actorID, tokenID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("revoke access token: context is required")
	}
	if accountID == uuid.Nil || actorID == uuid.Nil || tokenID == uuid.Nil {
		return fmt.Errorf("revoke access token: account, actor and token are required")
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	if err := s.requireActivity(); err != nil {
		return err
	}
	return s.activity.Within(ctx, func(ctx context.Context) error {
		token, err := s.tokens.FindByIDAndAccount(ctx, tokenID, accountID)
		if err != nil || token == nil {
			return ErrAccessTokenNotFound
		}
		if token.RevokedAt != nil {
			return nil
		}
		changed, err := s.tokens.MarkRevoked(ctx, tokenID, accountID)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		meta, err := activitylog.TokenRevoked(token.Name, token.Permissions)
		if err != nil {
			return err
		}
		id := accountID
		return s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &id,
			ActorUserID: actorID,
			Action:      activitylog.ActionTokenRevoked,
			TargetType:  activitylog.TargetAccessToken,
			TargetID:    tokenID.String(),
			Metadata:    meta,
		})
	})
}

func (s *Service) requireAccounts() error {
	if s.accounts == nil {
		return fmt.Errorf("account service: accounts repository is required")
	}
	return nil
}

// SignInAccount is one account of a signed-in user with the user's role on it.
type SignInAccount struct {
	Account models.Account
	Role    string
}

// SignInAccounts is what a login answers with: the user's accounts and which of
// them is the default. DefaultID is uuid.Nil when the user has no account.
type SignInAccounts struct {
	Accounts  []SignInAccount
	DefaultID uuid.UUID
}

// SignInAccounts lists the accounts the user belongs to, ordered by environment
// and then id because the store has no order and the list is part of the login
// body. The default is the user's own default account when they still belong to
// it, otherwise the first account. A membership whose account cannot be read is
// left out; a failed membership read is the error.
func (s *Service) SignInAccounts(ctx context.Context, userID uuid.UUID, defaultAccountID *uuid.UUID) (SignInAccounts, error) {
	memberships, err := s.ListMemberships(ctx, userID)
	if err != nil {
		return SignInAccounts{}, err
	}

	var result SignInAccounts
	for _, membership := range memberships {
		account, err := s.FindByID(ctx, membership.AccountID)
		if err != nil || account == nil {
			continue
		}
		result.Accounts = append(result.Accounts, SignInAccount{Account: *account, Role: membership.Role})
	}

	sort.Slice(result.Accounts, func(i, j int) bool {
		left, right := result.Accounts[i].Account, result.Accounts[j].Account
		if left.Environment != right.Environment {
			return left.Environment < right.Environment
		}
		return left.ID.String() < right.ID.String()
	})

	for _, entry := range result.Accounts {
		if defaultAccountID != nil && *defaultAccountID == entry.Account.ID {
			result.DefaultID = entry.Account.ID
		}
	}
	if result.DefaultID == uuid.Nil && len(result.Accounts) > 0 {
		result.DefaultID = result.Accounts[0].Account.ID
	}
	return result, nil
}
