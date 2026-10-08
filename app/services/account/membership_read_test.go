package account

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestAdd_User_DeniesWhenTheMembershipReadFails(t *testing.T) {
	accountID := uuid.New()
	actorID := uuid.New()
	targetID := uuid.New()
	store := &includeDeletedFails{actor: &models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: actorID,
		Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}}
	svc := NewService(Deps{Memberships: store})

	err := svc.AddUser(context.Background(), accountID, targetID, models.AccountRoleUser, actorID)
	if err == nil {
		t.Fatal("a failed membership read admitted the add")
	}
	if store.created {
		t.Fatal("a failed membership read wrote a membership")
	}
}

func TestGet_User_RoleDeniesWhenTheMembershipReadFails(t *testing.T) {
	store := roleReadFails{}
	svc := NewService(Deps{Memberships: store})

	role, err := svc.GetUserRole(context.Background(), uuid.New(), uuid.New())
	if err == nil || role != "" {
		t.Fatalf("GetUserRole(%q, %v) admitted a failed membership read", role, err)
	}
}

func TestGet_User_RoleReturnsEmptyWhenTheMembershipIsMissing(t *testing.T) {
	svc := NewService(Deps{Memberships: roleMissing{}})

	role, err := svc.GetUserRole(context.Background(), uuid.New(), uuid.New())
	if err != nil || role != "" {
		t.Fatalf("missing membership = %q, %v", role, err)
	}
}

type includeDeletedFails struct {
	MembershipStore
	actor   *models.AccountUser
	created bool
}

func (m *includeDeletedFails) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return m.actor, nil
}

func (m *includeDeletedFails) FindByAccountAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, errors.New("membership store unavailable")
}

func (m *includeDeletedFails) Create(context.Context, *models.AccountUser) error {
	m.created = true
	return nil
}

type roleReadFails struct{ MembershipStore }

func (roleReadFails) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, errors.New("membership store unavailable")
}

type roleMissing struct{ MembershipStore }

func (roleMissing) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, models.ErrRepositoryNotFound
}
