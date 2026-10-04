package account

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type lifecycleAdmins struct {
	allowed uuid.UUID
	err     error
}

func (a lifecycleAdmins) Contains(_ context.Context, userID uuid.UUID) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	return userID == a.allowed, nil
}

type lifecycleAccounts struct {
	row    *models.Account
	writes []string
	reads  int
}

func (a *lifecycleAccounts) Create(context.Context, *models.Account) error {
	return fmt.Errorf("create is not used")
}

func (a *lifecycleAccounts) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	a.reads++
	if a.row == nil || a.row.ID != id {
		return nil, models.ErrRepositoryNotFound
	}
	copy := *a.row
	return &copy, nil
}

func (a *lifecycleAccounts) SetName(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set name is not used")
}

func (a *lifecycleAccounts) SetViewAllWallets(context.Context, uuid.UUID, bool) error {
	return fmt.Errorf("set view_all_wallets is not used")
}

func (a *lifecycleAccounts) SetStatus(_ context.Context, id uuid.UUID, status string) error {
	if a.row == nil || a.row.ID != id {
		return models.ErrRepositoryNotFound
	}
	a.row.Status = status
	a.writes = append(a.writes, status)
	return nil
}

func (a *lifecycleAccounts) SetLinkedAccountID(context.Context, uuid.UUID, uuid.UUID) error {
	return fmt.Errorf("set linked account is not used")
}

func (a *lifecycleAccounts) PaginateByMember(context.Context, uuid.UUID, string, string, int, int) ([]models.Account, int64, error) {
	return nil, 0, fmt.Errorf("paginate is not used")
}

func TestSetPlatformLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actor := uuid.New()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	frozen, err := service.SetPlatformLifecycle(ctx, actor, accountID, models.AccountStatusFrozen)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Status != models.AccountStatusFrozen {
		t.Fatalf("status = %q", frozen.Status)
	}
	again, err := service.SetPlatformLifecycle(ctx, actor, accountID, models.AccountStatusFrozen)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != models.AccountStatusFrozen {
		t.Fatalf("repeat status = %q", again.Status)
	}
	if len(store.writes) != 1 || store.writes[0] != models.AccountStatusFrozen {
		t.Fatalf("writes = %v", store.writes)
	}

	active, err := service.SetPlatformLifecycle(ctx, actor, accountID, models.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != models.StatusActive {
		t.Fatalf("unfrozen status = %q", active.Status)
	}
	archived, err := service.SetPlatformLifecycle(ctx, actor, accountID, models.AccountStatusArchived)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != models.AccountStatusArchived {
		t.Fatalf("archived status = %q", archived.Status)
	}
	if len(store.writes) != 3 {
		t.Fatalf("writes = %v", store.writes)
	}
}

func TestSetPlatformLifecycleRefusesANonAdminBeforeReading(t *testing.T) {
	t.Parallel()
	store := &lifecycleAccounts{row: &models.Account{ID: uuid.New(), Status: models.StatusActive}}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: uuid.New()})
	_, err := service.SetPlatformLifecycle(context.Background(), uuid.New(), store.row.ID, models.AccountStatusFrozen)
	if !errors.Is(err, ErrPlatformLifecycleForbidden) {
		t.Fatalf("err = %v", err)
	}
	if store.reads != 0 || len(store.writes) != 0 || store.row.Status != models.StatusActive {
		t.Fatalf("reads %d writes %v status %s", store.reads, store.writes, store.row.Status)
	}
}

func TestSetPlatformLifecycleMissingAccountWritesNothing(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	store := &lifecycleAccounts{}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})
	_, err := service.SetPlatformLifecycle(context.Background(), actor, uuid.New(), models.AccountStatusArchived)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(store.writes) != 0 {
		t.Fatalf("writes = %v", store.writes)
	}
}

func TestSetPlatformLifecycleRejectsBadInput(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	accountID := uuid.New()
	store := &lifecycleAccounts{row: &models.Account{ID: accountID, Status: models.StatusActive}}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})
	ctx := context.Background()

	if _, err := service.SetPlatformLifecycle(nil, actor, accountID, models.AccountStatusFrozen); err == nil {
		t.Fatal("nil context")
	}
	if _, err := service.SetPlatformLifecycle(ctx, uuid.Nil, accountID, models.AccountStatusFrozen); err == nil {
		t.Fatal("nil actor")
	}
	if _, err := service.SetPlatformLifecycle(ctx, actor, uuid.Nil, models.AccountStatusFrozen); err == nil {
		t.Fatal("nil account")
	}
	if _, err := service.SetPlatformLifecycle(ctx, actor, accountID, "suspended"); !errors.Is(err, ErrAccountStatus) {
		t.Fatalf("bad status err = %v", err)
	}
	if len(store.writes) != 0 || store.reads != 0 {
		t.Fatalf("reads %d writes %v", store.reads, store.writes)
	}

	bare := NewService(Deps{Accounts: store})
	if _, err := bare.SetPlatformLifecycle(ctx, actor, accountID, models.AccountStatusFrozen); err == nil {
		t.Fatal("missing admins")
	}
	lookup := errors.New("lookup failed")
	failing := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{err: lookup})
	_, err := failing.SetPlatformLifecycle(ctx, actor, accountID, models.AccountStatusFrozen)
	if !errors.Is(err, lookup) {
		t.Fatalf("lookup err = %v", err)
	}
	if store.reads != 0 {
		t.Fatalf("reads = %d", store.reads)
	}
}
