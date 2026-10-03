package features

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestGateMissingRowAndOnProceedOffBlocks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  string
		code string
	}{
		{key: FlagWithdrawalsEnabled, code: CodeWithdrawalsPaused},
		{key: FlagSweepEnabled, code: CodeSweepPaused},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			store := newMemoryStore()
			service := NewService(store)
			accountID := uuid.New()
			ctx := context.Background()

			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("missing row: %v", err)
			}
			if _, ok := store.written(accountID, tc.key); ok {
				t.Fatal("gate inserted a row")
			}

			if _, err := service.Set(ctx, accountID, "owner", tc.key, true); err != nil {
				t.Fatalf("store on: %v", err)
			}
			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("flag on: %v", err)
			}

			if _, err := service.Set(ctx, accountID, "owner", tc.key, false); err != nil {
				t.Fatalf("store off: %v", err)
			}
			err := service.Gate(ctx, accountID, tc.key, tc.code)
			var gate *GateError
			if !errors.As(err, &gate) || gate.Code != tc.code {
				t.Fatalf("flag off: %v", err)
			}

			if _, err := service.Set(ctx, accountID, "owner", tc.key, true); err != nil {
				t.Fatalf("store on again: %v", err)
			}
			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("toggle on must apply on the next read: %v", err)
			}
		})
	}
}

func TestGateOffUsesTheGivenPauseCode(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	ctx := context.Background()

	if _, err := service.Set(ctx, accountID, "owner", FlagSweepEnabled, false); err != nil {
		t.Fatalf("store: %v", err)
	}
	err := service.Gate(ctx, accountID, FlagSweepEnabled, "")
	var gate *GateError
	if !errors.As(err, &gate) || gate.Code != FlagSweepEnabled {
		t.Fatalf("empty code = %v", err)
	}

	err = service.Gate(ctx, accountID, FlagSweepEnabled, CodeSweepPaused)
	if !errors.As(err, &gate) || gate.Code != CodeSweepPaused {
		t.Fatalf("pause code = %v", err)
	}
}

func TestGateNilAccountProceeds(t *testing.T) {
	t.Parallel()

	service := NewService(newMemoryStore())
	if err := service.Gate(context.Background(), uuid.Nil, FlagSweepEnabled, CodeSweepPaused); err != nil {
		t.Fatalf("nil account: %v", err)
	}
}
