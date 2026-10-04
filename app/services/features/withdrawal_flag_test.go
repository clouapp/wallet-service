package features

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/withdraw"
)

// Request is the withdrawal execution path. The container wires it to Gate,
// and a closed global row must stop it before Redis or a broadcast.
func TestWithdrawalRequestHonoursAClosedGlobalFlag(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	flags := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()
	if _, err := flags.Set(ctx, accountID, uuid.New(), "owner", FlagWithdrawalsEnabled, true); err != nil {
		t.Fatalf("account on: %v", err)
	}
	if err := store.UpsertGlobal(ctx, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("close global: %v", err)
	}

	worker := withdraw.NewService(nil, nil, nil, nil, nil, nil, nil, nil, func(ctx context.Context, id uuid.UUID) error {
		return flags.Gate(ctx, id, FlagWithdrawalsEnabled, CodeWithdrawalsPaused)
	})
	_, _, err := worker.Request(ctx, withdraw.WithdrawRequest{
		Passphrase:      "validpassphrase123",
		CallerAccountID: accountID,
	})
	var gate *GateError
	if !errors.As(err, &gate) || gate.Code != CodeWithdrawalsPaused {
		t.Fatalf("closed global: %v", err)
	}
	stored, ok := store.written(accountID, FlagWithdrawalsEnabled)
	if !ok || !stored {
		t.Fatal("the withdrawal path changed the account row")
	}

	if err := store.UpsertGlobal(ctx, FlagWithdrawalsEnabled, true); err != nil {
		t.Fatalf("reopen global: %v", err)
	}
	_, _, err = worker.Request(ctx, withdraw.WithdrawRequest{
		Passphrase:      "validpassphrase123",
		CallerAccountID: accountID,
	})
	if err == nil || !strings.Contains(err.Error(), "redis is not configured") {
		t.Fatalf("reopened global should pass the flag: %v", err)
	}
}
