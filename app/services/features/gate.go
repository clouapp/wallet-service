package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// CodeWithdrawalsPaused is the domain code §9.2 names for the withdrawals
// kill switch. Flags whose plan names no code use the flag key instead.
const CodeWithdrawalsPaused = "withdrawals_paused"

// GateError is the conflict a money-moving action returns when the account
// flag is stored enabled. Code is the HTTP error code.
type GateError struct {
	Code string
}

func (e *GateError) Error() string {
	if e == nil {
		return "feature flag gate"
	}
	return e.Code
}

// Enabled reads the stored boolean. A missing row is disabled (false). The
// table is read on every call; nothing is cached and no row is inserted.
func (s *Service) Enabled(ctx context.Context, accountID uuid.UUID, key string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("account features: service is required")
	}
	if ctx == nil {
		return false, fmt.Errorf("account features: context is required")
	}
	if accountID == uuid.Nil {
		return false, nil
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok {
		return false, ErrNotFound
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return false, err
	}
	return stored[key], nil
}

// Gate blocks the action when the flag is stored enabled. A missing row or
// an enabled=false row does not block, so the action proceeds. code is the
// conflict code; an empty code uses the flag key. The next call reads the
// row again, so turning the flag off applies without a restart.
func (s *Service) Gate(ctx context.Context, accountID uuid.UUID, key, code string) error {
	enabled, err := s.Enabled(ctx, accountID, key)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	code = strings.TrimSpace(code)
	if code == "" {
		code = strings.TrimSpace(key)
	}
	return &GateError{Code: code}
}
