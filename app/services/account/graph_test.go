package account_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// memState is the in-memory account graph. Within on the membership port
// snapshots it and restores the snapshot when the callback returns an error.
type memState struct {
	mu             sync.Mutex
	users          map[uuid.UUID]models.User
	accounts       map[uuid.UUID]models.Account
	members        map[uuid.UUID]models.AccountUser
	failMembership bool
}

func newMemState() *memState {
	return &memState{
		users:    map[uuid.UUID]models.User{},
		accounts: map[uuid.UUID]models.Account{},
		members:  map[uuid.UUID]models.AccountUser{},
	}
}

type graphSnap struct {
	users    map[uuid.UUID]models.User
	accounts map[uuid.UUID]models.Account
	members  map[uuid.UUID]models.AccountUser
}

func (s *memState) snapshot() graphSnap {
	return graphSnap{
		users:    copyUsers(s.users),
		accounts: copyAccounts(s.accounts),
		members:  copyMembers(s.members),
	}
}

func (s *memState) restore(snap graphSnap) {
	s.users = snap.users
	s.accounts = snap.accounts
	s.members = snap.members
}

func copyUsers(in map[uuid.UUID]models.User) map[uuid.UUID]models.User {
	out := make(map[uuid.UUID]models.User, len(in))
	for id, row := range in {
		out[id] = row
	}
	return out
}

func copyAccounts(in map[uuid.UUID]models.Account) map[uuid.UUID]models.Account {
	out := make(map[uuid.UUID]models.Account, len(in))
	for id, row := range in {
		out[id] = row
	}
	return out
}

func copyMembers(in map[uuid.UUID]models.AccountUser) map[uuid.UUID]models.AccountUser {
	out := make(map[uuid.UUID]models.AccountUser, len(in))
	for id, row := range in {
		out[id] = row
	}
	return out
}

func (s *memState) countAccounts(names ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	n := 0
	for _, account := range s.accounts {
		if want[account.Name] {
			n++
		}
	}
	return n
}

func (s *memState) countMembers(userID uuid.UUID, role, status string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, member := range s.members {
		if userID != uuid.Nil && member.UserID != userID {
			continue
		}
		if role != "" && member.Role != role {
			continue
		}
		if status != "" && member.Status != status {
			continue
		}
		n++
	}
	return n
}

func (s *memState) countUsers(email string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, user := range s.users {
		if user.Email == email {
			n++
		}
	}
	return n
}

type memAccounts struct{ state *memState }

func (a memAccounts) Create(_ context.Context, account *models.Account) error {
	if account == nil {
		return fmt.Errorf("create account: account is nil")
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.accounts[account.ID] = *account
	return nil
}

func (a memAccounts) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	account, ok := a.state.accounts[id]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return &account, nil
}

func (a memAccounts) SetName(_ context.Context, id uuid.UUID, name string) error {
	return a.update(id, func(account *models.Account) { account.Name = name })
}

func (a memAccounts) SetViewAllWallets(_ context.Context, id uuid.UUID, viewAll bool) error {
	return a.update(id, func(account *models.Account) { account.ViewAllWallets = viewAll })
}

func (a memAccounts) SetStatus(_ context.Context, id uuid.UUID, status string) error {
	return a.update(id, func(account *models.Account) { account.Status = status })
}

func (a memAccounts) SetLinkedAccountID(_ context.Context, id, linkedID uuid.UUID) error {
	return a.update(id, func(account *models.Account) { account.LinkedAccountID = &linkedID })
}

func (a memAccounts) update(id uuid.UUID, fn func(*models.Account)) error {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	account, ok := a.state.accounts[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	fn(&account)
	a.state.accounts[id] = account
	return nil
}

func (a memAccounts) PaginateByMember(context.Context, uuid.UUID, string, string, int, int) ([]models.Account, int64, error) {
	return nil, 0, fmt.Errorf("paginate accounts is not used by these tests")
}

func (a memAccounts) List(context.Context, int, int) ([]models.Account, int64, error) {
	return nil, 0, fmt.Errorf("list accounts is not used by these tests")
}

type memUsers struct{ state *memState }

func (u memUsers) FindByEmail(context.Context, string) (*models.User, error) {
	return nil, fmt.Errorf("find user by email is not used by these tests")
}

func (u memUsers) FindByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	user, ok := u.state.users[id]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return &user, nil
}

func (u memUsers) Create(_ context.Context, user *models.User) error {
	if user == nil {
		return fmt.Errorf("create user: user is nil")
	}
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	u.state.users[user.ID] = *user
	return nil
}

func (u memUsers) UpdateDefaultAccountID(_ context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	user, ok := u.state.users[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	if defaultAccountID == nil {
		user.DefaultAccountID = nil
	} else {
		copied := *defaultAccountID
		user.DefaultAccountID = &copied
	}
	u.state.users[id] = user
	return nil
}

type memMemberships struct{ state *memState }

func (m memMemberships) Create(_ context.Context, au *models.AccountUser) error {
	if au == nil {
		return fmt.Errorf("create membership: membership is nil")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.failMembership {
		return fmt.Errorf("membership insert failed")
	}
	m.state.members[au.ID] = *au
	return nil
}

func (m memMemberships) FindByAccountAndUser(_ context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	return m.find(accountID, userID, true)
}

func (m memMemberships) FindByAccountAndUserIncludeDeleted(_ context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	return m.find(accountID, userID, false)
}

func (m memMemberships) find(accountID, userID uuid.UUID, activeOnly bool) (*models.AccountUser, error) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	for _, member := range m.state.members {
		if member.AccountID != accountID || member.UserID != userID {
			continue
		}
		if activeOnly && (member.DeletedAt != nil || member.Status != models.StatusActive) {
			continue
		}
		copied := member
		return &copied, nil
	}
	return nil, models.ErrRepositoryNotFound
}

func (m memMemberships) PaginateByAccountID(context.Context, uuid.UUID, int, int) ([]models.AccountUser, int64, error) {
	return nil, 0, fmt.Errorf("paginate memberships is not used by these tests")
}

func (m memMemberships) ListForPlatformAccount(context.Context, uuid.UUID, int, int) ([]models.AccountUser, int64, error) {
	return nil, 0, fmt.Errorf("list platform memberships is not used by these tests")
}

func (m memMemberships) Restore(_ context.Context, id uuid.UUID) error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	member, ok := m.state.members[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	member.DeletedAt = nil
	m.state.members[id] = member
	return nil
}

func (m memMemberships) SetRole(_ context.Context, id uuid.UUID, role string) error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	member, ok := m.state.members[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	member.Role = role
	m.state.members[id] = member
	return nil
}

func (m memMemberships) SetStatus(_ context.Context, id uuid.UUID, status string) error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	member, ok := m.state.members[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	member.Status = status
	m.state.members[id] = member
	return nil
}

func (m memMemberships) SoftDeleteByAccountAndUser(_ context.Context, accountID, userID uuid.UUID) error {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	now := time.Now().UTC()
	for id, member := range m.state.members {
		if member.AccountID == accountID && member.UserID == userID && member.DeletedAt == nil {
			member.DeletedAt = &now
			m.state.members[id] = member
		}
	}
	return nil
}

func (m memMemberships) CountActiveByRole(context.Context, uuid.UUID, string) (int64, error) {
	return 0, fmt.Errorf("count memberships is not used by these tests")
}

func (m memMemberships) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("account user transaction: callback is required")
	}
	m.state.mu.Lock()
	snap := m.state.snapshot()
	m.state.mu.Unlock()
	if err := fn(ctx); err != nil {
		m.state.mu.Lock()
		m.state.restore(snap)
		m.state.mu.Unlock()
		return err
	}
	return nil
}

func (m memMemberships) FindByUserID(context.Context, uuid.UUID) ([]models.AccountUser, error) {
	return nil, fmt.Errorf("list user memberships is not used by these tests")
}

func (m memMemberships) RolesForUserAccounts(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, fmt.Errorf("roles for user accounts is not used by these tests")
}

func (m memMemberships) FindForOwnerAttach(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, fmt.Errorf("find membership for owner attach is not used by these tests")
}

func (m memMemberships) ActivateOwner(context.Context, uuid.UUID) error {
	return fmt.Errorf("activate owner is not used by these tests")
}
