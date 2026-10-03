package features

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// CodeWithdrawalsPaused is the domain code §9.2 names when withdrawals are
// off. CodeSweepPaused is the same shape for consolidate: the plan names the
// flag key sweep-enabled, not an error code, so the conflict names the pause.
const (
	CodeWithdrawalsPaused = "withdrawals_paused"
	CodeSweepPaused       = "sweep_paused"
)

// GateError is the conflict a money-moving action returns when the account
// flag is off. Code is the HTTP error code.
type GateError struct {
	Code string
}

func (e *GateError) Error() string {
	if e == nil {
		return "feature flag gate"
	}
	return e.Code
}

// Enabled reads the effective boolean. A stored row wins. A missing row is
// the catalog default, and nothing is inserted. The table is read on every
// call; nothing is cached. A nil account has no row to read and reports the
// catalog default without a query.
func (s *Service) Enabled(ctx context.Context, accountID uuid.UUID, key string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("account features: service is required")
	}
	if ctx == nil {
		return false, fmt.Errorf("account features: context is required")
	}
	key = strings.TrimSpace(key)
	definition, ok := Find(key)
	if !ok {
		return false, ErrNotFound
	}
	if accountID == uuid.Nil {
		return definition.Default, nil
	}
	stored, err := s.stored(ctx, accountID)
	if err != nil {
		return false, err
	}
	return enabledValue(stored, definition), nil
}

// Gate blocks the action when the account flag is off, and also when the
// global row for that key is explicitly false. A missing account row uses
// the catalog default, so withdrawals-enabled and sweep-enabled proceed
// until a row stores enabled=false. A missing global row, or a global row
// stored true, does not block. code is the conflict code; an empty code
// uses the flag key. The next call reads both rows again, so turning a flag
// off applies without a restart and without a cache. A nil account is not
// paused by the account check; an explicit global false still pauses it.
func (s *Service) Gate(ctx context.Context, accountID uuid.UUID, key, code string) error {
	if s == nil {
		return fmt.Errorf("account features: service is required")
	}
	if ctx == nil {
		return fmt.Errorf("account features: context is required")
	}
	key = strings.TrimSpace(key)
	if _, ok := Find(key); !ok {
		return ErrNotFound
	}
	blocked, err := s.globalExplicitlyOff(ctx, key)
	if err != nil {
		return err
	}
	if blocked {
		return pause(key, code)
	}
	if accountID == uuid.Nil {
		return nil
	}
	enabled, err := s.Enabled(ctx, accountID, key)
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}
	return pause(key, code)
}

func pause(key, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		code = strings.TrimSpace(key)
	}
	return &GateError{Code: code}
}
