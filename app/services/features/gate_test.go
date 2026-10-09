package features

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestGate_Missing_RowAndOnProceedOffBlocks(t *testing.T) {
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

func TestGate_Off_UsesTheGivenPauseCode(t *testing.T) {
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

func TestGate_Global_FalseBlocksEvenWhenTheAccountFlagIsOn(t *testing.T) {
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

func TestAccount_Override_SurvivesGlobalCloseAndReopen(t *testing.T) {
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

func TestGate_Account_OffStillBlocksWhenGlobalIsOn(t *testing.T) {
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

func TestGate_Global_MissingDoesNotBlock(t *testing.T) {
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

func TestGate_Nil_AccountProceeds(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore(), memoryAdmins{})
	if err := service.Gate(context.Background(), uuid.Nil, FlagSweepEnabled, CodeSweepPaused); err != nil {
		t.Fatalf("nil account: %v", err)
	}
}

// TestGate_Wallet_GatesOnTheWalletAccountElseTheCaller pins the account a
// wallet's money movement is gated on: the wallet's own account when it has
// one, otherwise the caller's account. Only the paused account is stored off.
func TestGate_Wallet_GatesOnTheWalletAccountElseTheCaller(t *testing.T) {
	t.Parallel()

	paused, open := uuid.New(), uuid.New()
	nilAccount := uuid.Nil
	cases := []struct {
		name   string
		wallet *models.Wallet
		caller uuid.UUID
		paused bool
	}{
		{"the wallet account wins over an open caller", &models.Wallet{AccountID: &paused}, open, true},
		{"the wallet account wins over a paused caller", &models.Wallet{AccountID: &open}, paused, false},
		{"a wallet without an account falls back to the caller", &models.Wallet{}, paused, true},
		{"a wallet on the nil account falls back to the caller", &models.Wallet{AccountID: &nilAccount}, paused, true},
		{"no wallet falls back to the caller", nil, paused, true},
		{"no wallet and no caller is the nil account", nil, uuid.Nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemoryStore()
			service := newTestService(store, memoryAdmins{})
			ctx := context.Background()
			if _, err := service.Set(ctx, paused, uuid.New(), "owner", FlagWithdrawalsEnabled, false); err != nil {
				t.Fatalf("pause: %v", err)
			}

			err := service.GateWallet(ctx, tc.wallet, tc.caller, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)

			var gate *GateError
			if tc.paused != (errors.As(err, &gate) && gate.Code == CodeWithdrawalsPaused) {
				t.Fatalf("paused = %t, err = %v", tc.paused, err)
			}
			if !tc.paused && err != nil {
				t.Fatalf("open: %v", err)
			}
		})
	}
}
