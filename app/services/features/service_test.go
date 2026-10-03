package features

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memoryStore struct {
	rows map[uuid.UUID]map[string]bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[uuid.UUID]map[string]bool{}}
}

func (s *memoryStore) ListAccount(_ context.Context, accountID uuid.UUID) ([]models.Feature, error) {
	values := s.rows[accountID]
	rows := make([]models.Feature, 0, len(values))
	for key, enabled := range values {
		rows = append(rows, models.Feature{AccountID: accountID, Key: key, Enabled: enabled})
	}
	return rows, nil
}

func (s *memoryStore) Upsert(_ context.Context, accountID uuid.UUID, key string, enabled bool) error {
	if s.rows[accountID] == nil {
		s.rows[accountID] = map[string]bool{}
	}
	s.rows[accountID][key] = enabled
	return nil
}

func (s *memoryStore) written(accountID uuid.UUID, key string) (bool, bool) {
	value, ok := s.rows[accountID][key]
	return value, ok
}

func TestListMissingRowUsesCatalogDefaultAndWritesNothing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()

	view, err := service.List(context.Background(), accountID, "auditor")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(view.Features) != len(All()) {
		t.Fatalf("features = %d, want %d", len(view.Features), len(All()))
	}
	for _, flag := range view.Features {
		definition, ok := Find(flag.Key)
		if !ok {
			t.Fatalf("list returned unknown key %s", flag.Key)
		}
		if flag.Enabled != definition.Default {
			t.Fatalf("%s enabled = %v, catalog default %v", flag.Key, flag.Enabled, definition.Default)
		}
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatalf("list inserted %d rows", len(store.rows[accountID]))
	}
}

func TestSetThenListReadsTheStoredBoolean(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	ctx := context.Background()

	written, err := service.Set(ctx, accountID, "owner", FlagWithdrawalsEnabled, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !written.Enabled || written.Key != FlagWithdrawalsEnabled {
		t.Fatalf("write response = %+v", written)
	}

	view, err := service.List(ctx, accountID, "admin")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("enabled flag read back as disabled")
	}
	for _, flag := range view.Features {
		if flag.Key == FlagWithdrawalsEnabled {
			continue
		}
		definition, ok := Find(flag.Key)
		if !ok || flag.Enabled != definition.Default {
			t.Fatalf("%s changed without a write: %+v", flag.Key, flag)
		}
	}

	written, err = service.Set(ctx, accountID, "admin", FlagWithdrawalsEnabled, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if written.Enabled {
		t.Fatal("disable response is still enabled")
	}
	view, err = service.List(ctx, accountID, "owner")
	if err != nil {
		t.Fatalf("list after disable: %v", err)
	}
	if flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("disabled flag read back as enabled")
	}
}

func TestSetRejectsUnknownKeyAndAuditorBeforeWriting(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	ctx := context.Background()

	_, err := service.Set(ctx, accountID, "owner", "not-a-flag", true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key error = %v", err)
	}
	if _, ok := store.written(accountID, "not-a-flag"); ok {
		t.Fatal("unknown key was stored")
	}

	_, err = service.Set(ctx, accountID, "auditor", FlagSweepEnabled, true)
	if !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("auditor error = %v", err)
	}
	_, err = service.Set(ctx, accountID, "user", FlagSweepEnabled, true)
	if !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("user error = %v", err)
	}
	if _, ok := store.written(accountID, FlagSweepEnabled); ok {
		t.Fatal("forbidden write was stored")
	}

	_, err = service.Set(ctx, accountID, "user", "not-a-flag", true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key for a user = %v, want not found before forbidden", err)
	}
}

func TestListHidesFlagsFromAUserAndFromAnotherAccount(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	otherID := uuid.New()
	ctx := context.Background()

	if _, err := service.List(ctx, accountID, "user"); !errors.Is(err, ErrViewForbidden) {
		t.Fatalf("user list error = %v", err)
	}
	if _, err := service.Set(ctx, accountID, "owner", FlagWalletCreationEnabled, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	other, err := service.List(ctx, otherID, "auditor")
	if err != nil {
		t.Fatalf("other account: %v", err)
	}
	if flagEnabled(t, other, FlagWalletCreationEnabled) {
		t.Fatal("another account inherited the flag")
	}

	if _, err := service.List(nil, accountID, "owner"); err == nil {
		t.Fatal("nil context was accepted")
	}
	if _, err := service.Set(ctx, uuid.Nil, "owner", FlagSweepEnabled, true); err == nil {
		t.Fatal("nil account id was accepted")
	}
}

func flagEnabled(t *testing.T, view List, key string) bool {
	t.Helper()
	for _, flag := range view.Features {
		if flag.Key == key {
			return flag.Enabled
		}
	}
	t.Fatalf("missing %s", key)
	return false
}
