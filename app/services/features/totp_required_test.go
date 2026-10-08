package features

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestUser2_FA_RequiredMissingRowIsDefaultFalseAndWritesNothing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	required, err := service.User2FARequired(ctx, accountID)
	if err != nil {
		t.Fatalf("required: %v", err)
	}
	if required {
		t.Fatal("missing flag row must use the catalog default false")
	}
	if _, ok := store.written(accountID, FlagUser2FARequired); ok {
		t.Fatal("read inserted an account row")
	}
	if _, ok := store.globalWritten(FlagUser2FARequired); ok {
		t.Fatal("read inserted a global row")
	}
	if _, ok := store.written(accountID, FlagWithdrawalsEnabled); ok {
		t.Fatal("read wrote an unrelated flag")
	}
}

func TestUser2_FA_RequiredAccountOrGlobalTrue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name    string
		global  *bool
		account *bool
		want    bool
	}{
		{name: "account true", account: boolPtr(true), want: true},
		{name: "account false", account: boolPtr(false), want: false},
		{name: "global true", global: boolPtr(true), want: true},
		{name: "global false does not cancel account true", global: boolPtr(false), account: boolPtr(true), want: true},
		{name: "global true applies when the account row is false", global: boolPtr(true), account: boolPtr(false), want: true},
		{name: "both false", global: boolPtr(false), account: boolPtr(false), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemoryStore()
			service := newTestService(store, memoryAdmins{})
			accountID := uuid.New()
			if tc.global != nil {
				if err := store.UpsertGlobal(ctx, FlagUser2FARequired, *tc.global); err != nil {
					t.Fatalf("global: %v", err)
				}
			}
			if tc.account != nil {
				if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagUser2FARequired, *tc.account); err != nil {
					t.Fatalf("account: %v", err)
				}
			}
			if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagWithdrawalsEnabled, false); err != nil {
				t.Fatalf("unrelated flag: %v", err)
			}

			required, err := service.User2FARequired(ctx, accountID)
			if err != nil {
				t.Fatalf("required: %v", err)
			}
			if required != tc.want {
				t.Fatalf("required = %v, want %v", required, tc.want)
			}
			withdrawals, ok := store.written(accountID, FlagWithdrawalsEnabled)
			if !ok || withdrawals {
				t.Fatalf("unrelated flag changed: present %v value %v", ok, withdrawals)
			}
		})
	}
}

func TestUser2_FA_RequiredDoesNotUseTheOffGate(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	err := service.Gate(ctx, accountID, FlagUser2FARequired, FlagUser2FARequired)
	var gate *GateError
	if !errors.As(err, &gate) {
		t.Fatal("Gate blocks a default-false flag; enrollment must not call it")
	}
	required, err := service.User2FARequired(ctx, accountID)
	if err != nil {
		t.Fatalf("required: %v", err)
	}
	if required {
		t.Fatal("missing row must stay off")
	}
}

func TestUser2_FA_RequiredRejectsNilAccount(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore(), memoryAdmins{})
	if _, err := service.User2FARequired(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil account")
	}
	if _, err := (*Service)(nil).User2FARequired(context.Background(), uuid.New()); err == nil {
		t.Fatal("nil service")
	}
}

func boolPtr(value bool) *bool {
	return &value
}
