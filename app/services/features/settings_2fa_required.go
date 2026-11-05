package features

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Settings2FARequired reports whether the settings-2fa-required flag is on at
// the moment of the call. A missing row uses the catalog default (false) and
// nothing is inserted. An explicit true on the global row or on the account
// row is enough; a global false does not cancel an account true. A route with
// no account (uuid.Nil, the /v1/platform group) reads only the global row.
func (s *Service) Settings2FARequired(ctx context.Context, accountID uuid.UUID) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("account features: service is required")
	}
	definition, ok := Find(FlagSettings2FARequired)
	if !ok {
		return false, ErrNotFound
	}
	globalOn, err := s.globalOn(ctx, definition)
	if err != nil {
		return false, err
	}
	if globalOn || accountID == uuid.Nil {
		return globalOn, nil
	}
	return s.Enabled(ctx, accountID, FlagSettings2FARequired)
}
