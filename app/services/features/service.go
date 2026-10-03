package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// Store reads and writes one account's flag rows. It does not know the catalog.
type Store interface {
	ListAccount(ctx context.Context, accountID uuid.UUID) ([]models.Feature, error)
	Upsert(ctx context.Context, accountID uuid.UUID, key string, enabled bool) error
}

// Service reads and writes account feature flags. A read with no row is the
// catalog default. A write is stored and then read back; the response is that
// row, not the request echoed before the write.
type Service struct {
	store Store
}

// NewService builds the account feature-flag service.
func NewService(store Store) *Service {
	if store == nil {
		panic("account features service: store is required")
	}
	return &Service{store: store}
}

// List returns every account flag the role may read. A missing row is the
// catalog default. The call does not insert rows.
func (s *Service) List(ctx context.Context, accountID uuid.UUID, role string) (List, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return List{}, err
	}
	if !policies.MayViewSettings(role) {
		return List{}, ErrViewForbidden
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return List{}, err
	}
	flags := make([]Flag, 0, len(catalog))
	for _, definition := range ForAccount() {
		flags = append(flags, Flag{
			Key:     definition.Key,
			Enabled: enabledValue(stored, definition),
		})
	}
	return List{Features: flags}, nil
}

// Set stores one flag and returns the row it just wrote. An unknown key is
// ErrNotFound before the permission check. Auditor and user are
// ErrUpdateForbidden and leave the table unchanged.
func (s *Service) Set(ctx context.Context, accountID uuid.UUID, role, key string, enabled bool) (Flag, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return Flag{}, err
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok || !accountFlag(key) {
		return Flag{}, ErrNotFound
	}
	if !policies.MayUpdateSettings(role) {
		return Flag{}, ErrUpdateForbidden
	}
	if err := s.store.Upsert(ctx, accountID, key, enabled); err != nil {
		return Flag{}, err
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return Flag{}, err
	}
	value, ok := stored[key]
	if !ok {
		return Flag{}, ErrNotStored
	}
	return Flag{Key: key, Enabled: value}, nil
}

func (s *Service) stored(ctx context.Context, accountID uuid.UUID) (map[string]bool, error) {
	rows, err := s.store.ListAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, known := Find(row.Key); !known {
			continue
		}
		out[row.Key] = row.Enabled
	}
	return out, nil
}

func accountFlag(key string) bool {
	definition, ok := Find(key)
	return ok && definition.AppliesTo(ScopeAccount)
}

func requireAccount(ctx context.Context, accountID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("account features: context is required")
	}
	if accountID == uuid.Nil {
		return fmt.Errorf("account features: account id is required")
	}
	return nil
}
