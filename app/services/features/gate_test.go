package features

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestGateMissingRowAndOffProceedOnBlocks(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	ctx := context.Background()

	if err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused); err != nil {
		t.Fatalf("missing row: %v", err)
	}
	if _, ok := store.written(accountID, FlagWithdrawalsEnabled); ok {
		t.Fatal("gate inserted a row")
	}

	if _, err := service.Set(ctx, accountID, "owner", FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("store off: %v", err)
	}
	if err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused); err != nil {
		t.Fatalf("flag off: %v", err)
	}

	if _, err := service.Set(ctx, accountID, "owner", FlagWithdrawalsEnabled, true); err != nil {
		t.Fatalf("store on: %v", err)
	}
	err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)
	var gate *GateError
	if !errors.As(err, &gate) || gate.Code != CodeWithdrawalsPaused {
		t.Fatalf("flag on: %v", err)
	}

	if _, err := service.Set(ctx, accountID, "owner", FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("store off again: %v", err)
	}
	if err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused); err != nil {
		t.Fatalf("toggle off must apply on the next read: %v", err)
	}
}

func TestGateUsesTheFlagKeyWhenNoCodeIsGiven(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := NewService(store)
	accountID := uuid.New()
	ctx := context.Background()

	if _, err := service.Set(ctx, accountID, "owner", FlagSweepEnabled, true); err != nil {
		t.Fatalf("store: %v", err)
	}
	err := service.Gate(ctx, accountID, FlagSweepEnabled, "")
	var gate *GateError
	if !errors.As(err, &gate) || gate.Code != FlagSweepEnabled {
		t.Fatalf("code = %v", err)
	}
}

func TestGateNilAccountProceeds(t *testing.T) {
	t.Parallel()

	service := NewService(newMemoryStore())
	if err := service.Gate(context.Background(), uuid.Nil, FlagSweepEnabled, FlagSweepEnabled); err != nil {
		t.Fatalf("nil account: %v", err)
	}
}
