package account

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type ownerUsers struct {
	row     *models.User
	lookups int
	creates int
}

func (u *ownerUsers) FindByEmail(_ context.Context, email string) (*models.User, error) {
	u.lookups++
	if u.row == nil || u.row.Email != email {
		return nil, models.ErrRepositoryNotFound
	}
	copy := *u.row
	return &copy, nil
}

func (u *ownerUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	return nil, fmt.Errorf("find user by id is not used")
}

func (u *ownerUsers) Create(context.Context, *models.User) error {
	u.creates++
	return fmt.Errorf("create user is not used")
}

func (u *ownerUsers) UpdateDefaultAccountID(context.Context, uuid.UUID, *uuid.UUID) error {
	return fmt.Errorf("update default account is not used")
}

type ownerMemberships struct {
	row         *models.AccountUser
	creates     []*models.AccountUser
	activations int
	attachFinds int
	activeFinds int
}

func (m *ownerMemberships) Create(_ context.Context, au *models.AccountUser) error {
	if au == nil {
		return fmt.Errorf("create membership: row is nil")
	}
	copy := *au
	m.creates = append(m.creates, &copy)
	m.row = &copy
	return nil
}

func (m *ownerMemberships) FindByAccountAndUser(_ context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	m.activeFinds++
	if m.row == nil || m.row.AccountID != accountID || m.row.UserID != userID || m.row.DeletedAt != nil {
		return nil, models.ErrRepositoryNotFound
	}
	if m.row.Status != models.MembershipStatusActive && m.row.Status != "" {
		return nil, models.ErrRepositoryNotFound
	}
	copy := *m.row
	return &copy, nil
}

func (m *ownerMemberships) FindByAccountAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, fmt.Errorf("find deleted membership is not used")
}

func (m *ownerMemberships) PaginateByAccountID(context.Context, uuid.UUID, int, int) ([]models.AccountUser, int64, error) {
	return nil, 0, fmt.Errorf("paginate is not used")
}

func (m *ownerMemberships) ListForPlatformAccount(context.Context, uuid.UUID, int, int) ([]models.AccountUser, int64, error) {
	return nil, 0, fmt.Errorf("list is not used")
}

func (m *ownerMemberships) Restore(context.Context, uuid.UUID) error {
	return fmt.Errorf("restore is not used")
}

func (m *ownerMemberships) SetRole(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set role is not used")
}

func (m *ownerMemberships) SetStatus(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set membership status is not used")
}

func (m *ownerMemberships) SoftDeleteByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) error {
	return fmt.Errorf("soft delete is not used")
}

func (m *ownerMemberships) CountActiveByRole(context.Context, uuid.UUID, string) (int64, error) {
	return 0, fmt.Errorf("count is not used")
}

func (m *ownerMemberships) Within(context.Context, func(context.Context) error) error {
	return fmt.Errorf("transaction is not used")
}

func (m *ownerMemberships) FindByUserID(context.Context, uuid.UUID) ([]models.AccountUser, error) {
	return nil, fmt.Errorf("find by user is not used")
}

func (m *ownerMemberships) RolesForUserAccounts(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, fmt.Errorf("roles are not used")
}

func (m *ownerMemberships) FindForOwnerAttach(_ context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	m.attachFinds++
	if m.row == nil || m.row.AccountID != accountID || m.row.UserID != userID {
		return nil, models.ErrRepositoryNotFound
	}
	copy := *m.row
	return &copy, nil
}

func (m *ownerMemberships) ActivateOwner(_ context.Context, id uuid.UUID) error {
	m.activations++
	if m.row == nil || m.row.ID != id {
		return fmt.Errorf("activate owner: membership is missing")
	}
	m.row.Role = models.AccountRoleOwner
	m.row.Status = models.MembershipStatusActive
	m.row.DeletedAt = nil
	return nil
}

func TestAttach_Owner_ForPlatformRefusesANonAdminBeforeReadingTheAccount(t *testing.T) {
	t.Parallel()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.AccountStatusFrozen}}
	members := &ownerMemberships{row: &models.AccountUser{ID: uuid.New(), AccountID: accountID}}
	users := &ownerUsers{row: &models.User{ID: uuid.New(), Email: "ada@example.com", PasswordHash: "secret"}}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{})

	member, changed, err := service.AttachOwnerForPlatform(context.Background(), uuid.New(), accountID, "ada@example.com")

	if !errors.Is(err, ErrPlatformOwnersForbidden) || changed || member != nil {
		t.Fatalf("member %v changed %v err %v", member, changed, err)
	}
	if store.reads != 1 || users.lookups != 0 || users.creates != 0 || members.attachFinds != 0 || len(members.creates) != 0 || members.activations != 0 || len(store.writes) != 0 {
		t.Fatalf("reads %d lookups %d creates %d attach %d memberships %d activations %d writes %v", store.reads, users.lookups, users.creates, members.attachFinds, len(members.creates), members.activations, store.writes)
	}
}

func TestAttach_Owner_ForPlatformUnknownAccountDoesNotReadTheUser(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: uuid.New(), Status: models.StatusActive}}
	members := &ownerMemberships{}
	users := &ownerUsers{row: &models.User{ID: uuid.New(), Email: "ada@example.com"}}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	_, changed, err := service.AttachOwnerForPlatform(context.Background(), actor, uuid.New(), "ada@example.com")

	if !errors.Is(err, ErrAccountNotFound) || changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	if store.reads != 1 || users.lookups != 0 || members.attachFinds != 0 {
		t.Fatalf("reads %d lookups %d attach %d", store.reads, users.lookups, members.attachFinds)
	}
}

func TestAttach_Owner_ForPlatformUnknownUserDoesNotWriteAMembership(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &ownerMemberships{}
	users := &ownerUsers{}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	_, changed, err := service.AttachOwnerForPlatform(context.Background(), actor, accountID, "missing@example.com")

	if !errors.Is(err, ErrPlatformOwnerUserNotFound) || changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	if users.lookups != 1 || users.creates != 0 || members.attachFinds != 0 || len(members.creates) != 0 {
		t.Fatalf("lookups %d user creates %d attach %d memberships %d", users.lookups, users.creates, members.attachFinds, len(members.creates))
	}
}

func TestAttach_Owner_ForPlatformCreatesAnOwnerWithoutApplyingRank(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.AccountStatusFrozen}}
	members := &ownerMemberships{}
	users := &ownerUsers{row: &models.User{
		ID: userID, Email: "ada@example.com", FullName: "Ada", Status: "active",
		PasswordHash: "secret", TotpSecret: "totp", TotpEnabled: true,
	}}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	member, changed, err := service.AttachOwnerForPlatform(context.Background(), actor, accountID, " ada@example.com ")
	if err != nil || !changed || member == nil {
		t.Fatalf("member %v changed %v err %v", member, changed, err)
	}
	if len(members.creates) != 1 || members.activations != 0 || len(store.writes) != 0 {
		t.Fatalf("creates %d activations %d status writes %v", len(members.creates), members.activations, store.writes)
	}
	created := members.creates[0]
	if created.Role != models.AccountRoleOwner || created.Status != models.MembershipStatusActive || created.UserID != userID || created.AccountID != accountID {
		t.Fatalf("created %+v", created)
	}
	if created.AddedBy == nil || *created.AddedBy != actor {
		t.Fatalf("added_by %v", created.AddedBy)
	}
	if member.User == nil || member.User.PasswordHash != "" || member.User.TotpSecret != "" || !member.User.TotpEnabled || member.User.Email != "ada@example.com" {
		t.Fatalf("user %+v", member.User)
	}
}

func TestAttach_Owner_ForPlatformDoesNotWriteWhenTheUserIsAlreadyTheActiveOwner(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()
	existingID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	members := &ownerMemberships{row: &models.AccountUser{
		ID: existingID, AccountID: accountID, UserID: userID,
		Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}}
	users := &ownerUsers{row: &models.User{ID: userID, Email: "ada@example.com", PasswordHash: "secret"}}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	member, changed, err := service.AttachOwnerForPlatform(context.Background(), actor, accountID, "ada@example.com")
	if err != nil || changed || member == nil || member.ID != existingID {
		t.Fatalf("member %+v changed %v err %v", member, changed, err)
	}
	if len(members.creates) != 0 || members.activations != 0 || members.activeFinds != 0 {
		t.Fatalf("creates %d activations %d active finds %d", len(members.creates), members.activations, members.activeFinds)
	}
	if member.User == nil || member.User.PasswordHash != "" {
		t.Fatalf("user %+v", member.User)
	}
}

func TestAttach_Owner_ForPlatformRestoresAMembershipAsOwner(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().UTC()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.AccountStatusFrozen}}
	members := &ownerMemberships{row: &models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID,
		Role: "admin", Status: models.MembershipStatusSuspended, DeletedAt: &deletedAt,
	}}
	users := &ownerUsers{row: &models.User{ID: userID, Email: "ada@example.com"}}
	service := NewService(Deps{Accounts: store, Memberships: members, Users: users}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	member, changed, err := service.AttachOwnerForPlatform(context.Background(), actor, accountID, "ada@example.com")
	if err != nil || !changed || member == nil {
		t.Fatalf("member %+v changed %v err %v", member, changed, err)
	}
	if len(members.creates) != 0 || members.activations != 1 || len(store.writes) != 0 {
		t.Fatalf("creates %d activations %d writes %v", len(members.creates), members.activations, store.writes)
	}
	if member.Role != models.AccountRoleOwner || member.Status != models.MembershipStatusActive || member.DeletedAt != nil {
		t.Fatalf("stored %+v", member)
	}
}
