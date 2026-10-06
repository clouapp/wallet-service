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

type listAccounts struct {
	rows   []models.Account
	total  int64
	limit  int
	offset int
	called bool
	err    error
}

func (a *listAccounts) Create(context.Context, *models.Account) error {
	return fmt.Errorf("create is not used")
}

func (a *listAccounts) FindByID(context.Context, uuid.UUID) (*models.Account, error) {
	return nil, fmt.Errorf("find is not used")
}

func (a *listAccounts) SetName(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set name is not used")
}

func (a *listAccounts) SetViewAllWallets(context.Context, uuid.UUID, bool) error {
	return fmt.Errorf("set view_all_wallets is not used")
}

func (a *listAccounts) SetStatus(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("set status is not used")
}

func (a *listAccounts) SetLinkedAccountID(context.Context, uuid.UUID, uuid.UUID) error {
	return fmt.Errorf("set linked account is not used")
}

func (a *listAccounts) PaginateByMember(context.Context, uuid.UUID, string, string, int, int) ([]models.Account, int64, error) {
	return nil, 0, fmt.Errorf("paginate is not used")
}

func (a *listAccounts) List(_ context.Context, limit, offset int) ([]models.Account, int64, error) {
	a.called = true
	a.limit = limit
	a.offset = offset
	if a.err != nil {
		return nil, 0, a.err
	}
	return a.rows, a.total, nil
}

func TestList_For_PlatformRefusesACallerWhoIsNotAPlatformAdmin(t *testing.T) {
	t.Parallel()
	store := &listAccounts{rows: []models.Account{{ID: uuid.New(), Name: "Hidden"}}}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{})

	rows, total, err := service.ListForPlatform(context.Background(), uuid.New(), 20, 0)

	if !errors.Is(err, ErrPlatformViewForbidden) {
		t.Fatalf("err = %v", err)
	}
	if rows != nil || total != 0 || store.called {
		t.Fatalf("rows %v total %d called %v", rows, total, store.called)
	}
}

func TestList_For_PlatformReturnsThePageForAPlatformAdmin(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	newer := models.Account{ID: uuid.New(), Name: "Newer", Status: models.StatusActive}
	store := &listAccounts{rows: []models.Account{newer}, total: 4}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	rows, total, err := service.ListForPlatform(context.Background(), actor, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(rows) != 1 || rows[0].ID != newer.ID || store.limit != 1 || store.offset != 2 {
		t.Fatalf("rows %v total %d limit %d offset %d", rows, total, store.limit, store.offset)
	}
}

func TestList_For_PlatformRejectsAMissingActorAndABadPage(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	store := &listAccounts{}
	service := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{allowed: actor})

	if _, _, err := service.ListForPlatform(nil, actor, 20, 0); err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("nil context err = %v", err)
	}
	if _, _, err := service.ListForPlatform(context.Background(), uuid.Nil, 20, 0); err == nil || !strings.Contains(err.Error(), "actor is required") {
		t.Fatalf("nil actor err = %v", err)
	}
	if _, _, err := service.ListForPlatform(context.Background(), actor, 0, 0); err == nil || !strings.Contains(err.Error(), "limit and offset are invalid") {
		t.Fatalf("bad limit err = %v", err)
	}
	if _, _, err := service.ListForPlatform(context.Background(), actor, 20, -1); err == nil || !strings.Contains(err.Error(), "limit and offset are invalid") {
		t.Fatalf("bad offset err = %v", err)
	}
	if store.called {
		t.Fatal("store was read")
	}

	bare := NewService(Deps{Accounts: store})
	if _, _, err := bare.ListForPlatform(context.Background(), actor, 20, 0); err == nil || !strings.Contains(err.Error(), "platform admins are required") {
		t.Fatalf("missing admins err = %v", err)
	}
	if store.called {
		t.Fatal("store was read without an admin gate")
	}

	lookup := errors.New("lookup failed")
	failing := NewService(Deps{Accounts: store}).WithPlatformAdmins(lifecycleAdmins{err: lookup})
	if _, _, err := failing.ListForPlatform(context.Background(), actor, 20, 0); !errors.Is(err, lookup) || store.called {
		t.Fatalf("lookup err = %v called %v", err, store.called)
	}
}
