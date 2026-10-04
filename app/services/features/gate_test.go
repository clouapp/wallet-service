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
			service := newTestService(store, memoryAdmins{})
			accountID := uuid.New()
			ctx := context.Background()

			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("missing row: %v", err)
			}
			if _, ok := store.written(accountID, tc.key); ok {
				t.Fatal("gate inserted a row")
			}

			if _, err := service.Set(ctx, accountID, uuid.New(), "owner", tc.key, true); err != nil {
				t.Fatalf("store on: %v", err)
			}
			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("flag on: %v", err)
			}

			if _, err := service.Set(ctx, accountID, uuid.New(), "owner", tc.key, false); err != nil {
				t.Fatalf("store off: %v", err)
			}
			err := service.Gate(ctx, accountID, tc.key, tc.code)
			var gate *GateError
			if !errors.As(err, &gate) || gate.Code != tc.code {
				t.Fatalf("flag off: %v", err)
			}

			if _, err := service.Set(ctx, accountID, uuid.New(), "owner", tc.key, true); err != nil {
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
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagSweepEnabled, false); err != nil {
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

func TestGateGlobalFalseBlocksEvenWhenTheAccountFlagIsOn(t *testing.T) {
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
			service := newTestService(store, memoryAdmins{})
			accountID := uuid.New()
			ctx := context.Background()

			if _, err := service.Set(ctx, accountID, uuid.New(), "owner", tc.key, true); err != nil {
				t.Fatalf("account on: %v", err)
			}
			if err := store.UpsertGlobal(ctx, tc.key, false); err != nil {
				t.Fatalf("global off: %v", err)
			}
			err := service.Gate(ctx, accountID, tc.key, tc.code)
			var gate *GateError
			if !errors.As(err, &gate) || gate.Code != tc.code {
				t.Fatalf("global off: %v", err)
			}

			if err := store.UpsertGlobal(ctx, tc.key, true); err != nil {
				t.Fatalf("global on: %v", err)
			}
			if err := service.Gate(ctx, accountID, tc.key, tc.code); err != nil {
				t.Fatalf("global on must release the veto on the next read: %v", err)
			}
		})
	}
}

func TestAccountOverrideSurvivesGlobalCloseAndReopen(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	adminID := uuid.New()
	service := newTestService(store, memoryAdmins{users: map[uuid.UUID]struct{}{adminID: {}}})
	accountID := uuid.New()
	ctx := context.Background()

	if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("account override: %v", err)
	}
	if _, err := service.SetGlobal(ctx, adminID, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("close global: %v", err)
	}
	closed, ok := store.written(accountID, FlagWithdrawalsEnabled)
	if !ok || closed {
		t.Fatal("closing the global flag changed the account override")
	}
	var gate *GateError
	err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)
	if !errors.As(err, &gate) || gate.Code != CodeWithdrawalsPaused {
		t.Fatalf("closed global: %v", err)
	}

	if _, err := service.SetGlobal(ctx, adminID, FlagWithdrawalsEnabled, true); err != nil {
		t.Fatalf("reopen global: %v", err)
	}
	reopened, ok := store.written(accountID, FlagWithdrawalsEnabled)
	if !ok || reopened {
		t.Fatal("reopening the global flag changed the account override")
	}
	global, ok := store.globalWritten(FlagWithdrawalsEnabled)
	if !ok || !global {
		t.Fatal("global row was not reopened")
	}
	err = service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)
	if !errors.As(err, &gate) || gate.Code != CodeWithdrawalsPaused {
		t.Fatalf("account override after reopen: %v", err)
	}
}

func TestGateAccountOffStillBlocksWhenGlobalIsOn(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	if err := store.UpsertGlobal(ctx, FlagWithdrawalsEnabled, true); err != nil {
		t.Fatalf("global on: %v", err)
	}
	if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("account off: %v", err)
	}
	err := service.Gate(ctx, accountID, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)
	var gate *GateError
	if !errors.As(err, &gate) || gate.Code != CodeWithdrawalsPaused {
		t.Fatalf("account off with global on: %v", err)
	}
}

func TestGateGlobalMissingDoesNotBlock(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	if err := service.Gate(ctx, accountID, FlagSweepEnabled, CodeSweepPaused); err != nil {
		t.Fatalf("missing global and missing account: %v", err)
	}
	if _, ok := store.globalWritten(FlagSweepEnabled); ok {
		t.Fatal("gate inserted a global row")
	}
}

func TestGateNilAccountProceeds(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore(), memoryAdmins{})
	if err := service.Gate(context.Background(), uuid.Nil, FlagSweepEnabled, CodeSweepPaused); err != nil {
		t.Fatalf("nil account: %v", err)
	}
}
