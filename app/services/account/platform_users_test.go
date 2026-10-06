package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type platformUserMemberships struct {
	rows    []models.AccountUser
	total   int64
	limit   int
	offset  int
	called  bool
	account uuid.UUID
	err     error
}

func (m *platformUserMemberships) Create(context.Context, *models.AccountUser) error {
	return fmt.Errorf("create is not used")
}

func (m *platformUserMemberships) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, fmt.Errorf("find membership is not used")
}

func (m *platformUserMemberships) FindByAccountAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, fmt.Errorf("find deleted membership is not used")
}

func (m *platformUserMemberships) PaginateByAccountID(context.Context, uuid.UUID, int, int) ([]models.AccountUser, int64, error) {
	return nil, 0, fmt.Errorf("paginate is not used")
}

func (m *platformUserMemberships) ListForPlatformAccount(_ context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	m.called = true
	m.account = accountID
	m.limit = limit
	m.offset = offset
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.rows, m.total, nil
}

func (m *platformUserMemberships) Restore(context.Context, uuid.UUID) error {
	return fmt.Errorf("restore is not used")
}

func (m *platformUserMemberships) SetRole(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set role is not used")
}

func (m *platformUserMemberships) SetStatus(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set membership status is not used")
}

func (m *platformUserMemberships) SoftDeleteByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) error {
	return fmt.Errorf("soft delete is not used")
}

func (m *platformUserMemberships) CountActiveByRole(context.Context, uuid.UUID, string) (int64, error) {
	return 0, fmt.Errorf("count is not used")
}

func (m *platformUserMemberships) Within(context.Context, func(context.Context) error) error {
	return fmt.Errorf("transaction is not used")
}

func (m *platformUserMemberships) FindByUserID(context.Context, uuid.UUID) ([]models.AccountUser, error) {
	return nil, fmt.Errorf("find by user is not used")
}

func (m *platformUserMemberships) RolesForUserAccounts(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, fmt.Errorf("roles are not used")
}

func (m *platformUserMemberships) FindForOwnerAttach(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, fmt.Errorf("find for owner attach is not used")
}

func (m *platformUserMemberships) ActivateOwner(context.Context, uuid.UUID) error {
	return fmt.Errorf("activate owner is not used")
}

func TestList_Users_ForPlatformRefusesANonAdminBeforeReadingTheAccount(t *testing.T) {
	t.Parallel()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &platformUserMemberships{rows: []models.AccountUser{{ID: uuid.New(), AccountID: accountID}}}
	service := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{})

	rows, total, err := service.ListUsersForPlatform(context.Background(), uuid.New(), accountID, 20, 0)

	if !errors.Is(err, ErrPlatformAccountUsersForbidden) {
		t.Fatalf("err = %v", err)
	}
	if rows != nil || total != 0 || store.reads != 1 || members.called {
		t.Fatalf("rows %v total %d reads %d called %v", rows, total, store.reads, members.called)
	}

	missing := &lifecycleAccounts{}
	rows, total, err = NewService(Deps{Accounts: missing, Memberships: &platformUserMemberships{}}).
		WithPlatformAdmins(lifecycleAdmins{}).
		ListUsersForPlatform(context.Background(), uuid.New(), uuid.New(), 20, 0)
	if !errors.Is(err, ErrAccountNotFound) || rows != nil || total != 0 || missing.reads != 1 {
		t.Fatalf("missing account rows %v total %d reads %d err %v", rows, total, missing.reads, err)
	}
}

func TestList_Users_ForPlatformUnknownAccountDoesNotReadMemberships(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: uuid.New(), Status: models.StatusActive}}
	members := &platformUserMemberships{}
	service := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	_, _, err := service.ListUsersForPlatform(context.Background(), actor, uuid.New(), 20, 0)

	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v", err)
	}
	if store.reads != 1 || members.called {
		t.Fatalf("reads %d called %v", store.reads, members.called)
	}
}

func TestList_Users_ForPlatformReturnsThePageForAPlatformAdmin(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()
	member := models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: models.StatusActive,
		User: &models.User{ID: userID, Email: "ada@example.com"},
	}
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &platformUserMemberships{rows: []models.AccountUser{member}, total: 4}
	service := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	rows, total, err := service.ListUsersForPlatform(context.Background(), actor, accountID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(rows) != 1 || rows[0].ID != member.ID || members.limit != 1 || members.offset != 2 || members.account != accountID {
		t.Fatalf("rows %v total %d limit %d offset %d account %s", rows, total, members.limit, members.offset, members.account)
	}
	if store.reads != 1 {
		t.Fatalf("reads %d", store.reads)
	}
}

func TestList_Users_ForPlatformRejectsARowWithoutAUser(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &platformUserMemberships{rows: []models.AccountUser{{ID: uuid.New(), AccountID: accountID}}}
	service := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	_, _, err := service.ListUsersForPlatform(context.Background(), actor, accountID, 20, 0)
	if err == nil || !strings.Contains(err.Error(), "has no user") {
		t.Fatalf("err = %v", err)
	}
}

func TestList_Users_ForPlatformRejectsBadInputBeforeReading(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &platformUserMemberships{}
	service := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})
	ctx := context.Background()

	if _, _, err := service.ListUsersForPlatform(nil, actor, accountID, 20, 0); err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("nil context err = %v", err)
	}
	if _, _, err := service.ListUsersForPlatform(ctx, uuid.Nil, accountID, 20, 0); err == nil || !strings.Contains(err.Error(), "actor is required") {
		t.Fatalf("nil actor err = %v", err)
	}
	if _, _, err := service.ListUsersForPlatform(ctx, actor, uuid.Nil, 20, 0); err == nil || !strings.Contains(err.Error(), "account id is required") {
		t.Fatalf("nil account err = %v", err)
	}
	if _, _, err := service.ListUsersForPlatform(ctx, actor, accountID, 0, 0); err == nil || !strings.Contains(err.Error(), "limit and offset are invalid") {
		t.Fatalf("bad limit err = %v", err)
	}
	if _, _, err := service.ListUsersForPlatform(ctx, actor, accountID, 20, -1); err == nil || !strings.Contains(err.Error(), "limit and offset are invalid") {
		t.Fatalf("bad offset err = %v", err)
	}
	if store.reads != 0 || members.called {
		t.Fatalf("reads %d called %v", store.reads, members.called)
	}

	bare := NewService(Deps{Accounts: store, Memberships: members})
	if _, _, err := bare.ListUsersForPlatform(ctx, actor, accountID, 20, 0); err == nil || !strings.Contains(err.Error(), "platform admins are required") {
		t.Fatalf("missing admins err = %v", err)
	}
	if store.reads != 0 || members.called {
		t.Fatal("store was read without an admin gate")
	}

	lookup := errors.New("lookup failed")
	failing := NewService(Deps{Accounts: store, Memberships: members}).WithPlatformAdmins(lifecycleAdmins{err: lookup})
	if _, _, err := failing.ListUsersForPlatform(ctx, actor, accountID, 20, 0); !errors.Is(err, lookup) || store.reads != 1 || members.called {
		t.Fatalf("lookup err = %v reads %d called %v", err, store.reads, members.called)
	}
}
