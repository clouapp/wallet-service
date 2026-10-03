package features

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// User2FARequired reports whether the user-2fa-required flag is on for this
// account at the moment of the call. A missing global row and a missing
// account row each use the catalog default (false) and nothing is inserted.
// An explicit true on either scope is enough: the global row is the platform
// rollout, the account row is the rollout for this account. An explicit
// global false does not cancel an account true. This is not Gate: Gate
// blocks when a flag is off, and this flag's default is off.
func (s *Service) User2FARequired(ctx context.Context, accountID uuid.UUID) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("account features: service is required")
	}
	if err := requireAccount(ctx, accountID); err != nil {
		return false, err
	}
	definition, ok := Find(FlagUser2FARequired)
	if !ok {
		return false, ErrNotFound
	}
	globalOn, err := s.globalOn(ctx, definition)
	if err != nil {
		return false, err
	}
	if globalOn {
		return true, nil
	}
	return s.Enabled(ctx, accountID, FlagUser2FARequired)
}

func (s *Service) globalOn(ctx context.Context, definition Definition) (bool, error) {
	enabled, found, err := s.store.GetGlobal(ctx, definition.Key)
	if err != nil {
		return false, err
	}
	if !found {
		return definition.Default, nil
	}
	return enabled, nil
}
