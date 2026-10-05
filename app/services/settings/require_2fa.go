package settings

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// Require2FA reads account_security.require_2fa at the moment of use.
// A cache hit skips the database. A cache miss, a cache failure, or a value
// that is not a JSON object reads the database. A missing row and a blank value use the
// registry default (false) and nothing is inserted. A database failure is
// returned. session_idle_minutes is stored in the same group and is not read here.
func (s *Service) Require2FA(ctx context.Context, accountID uuid.UUID) (bool, error) {
	if s == nil {
		return false, errServiceRequired
	}
	if err := requireAccount(ctx, accountID); err != nil {
		return false, err
	}
	group, ok := FindGroup(groupAccountSecurity)
	if !ok {
		return false, ErrGroupNotFound
	}
	stored, err := s.storedValues(ctx, accountID, group.Name)
	if err != nil {
		return false, err
	}
	raw, ok := stored[keyRequire2FA]
	if !ok {
		return false, nil
	}
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false", "":
		return false, nil
	default:
		slog.Warn("account security require_2fa fell back to the registry default", "account_id", accountID)
		return false, nil
	}
}
