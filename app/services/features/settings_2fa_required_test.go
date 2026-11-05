package features

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSettings2_FA_RequiredMissingRowIsDefaultFalseAndWritesNothing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	for _, id := range []uuid.UUID{accountID, uuid.Nil} {
		required, err := service.Settings2FARequired(ctx, id)
		if err != nil {
			t.Fatalf("required: %v", err)
		}
		if required {
			t.Fatal("missing flag row must use the catalog default false")
		}
	}
	if _, ok := store.written(accountID, FlagSettings2FARequired); ok {
		t.Fatal("read inserted an account row")
	}
	if _, ok := store.globalWritten(FlagSettings2FARequired); ok {
		t.Fatal("read inserted a global row")
	}
}

func TestSettings2_FA_RequiredAccountOrGlobalTrue(t *testing.T) {
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
				if err := store.UpsertGlobal(ctx, FlagSettings2FARequired, *tc.global); err != nil {
					t.Fatalf("global: %v", err)
				}
			}
			if tc.account != nil {
				if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagSettings2FARequired, *tc.account); err != nil {
					t.Fatalf("account: %v", err)
				}
			}
			required, err := service.Settings2FARequired(ctx, accountID)
			if err != nil {
				t.Fatalf("required: %v", err)
			}
			if required != tc.want {
				t.Fatalf("required = %v, want %v", required, tc.want)
			}
		})
	}
}

func TestSettings2_FA_RequiredWithoutAnAccountReadsOnlyTheGlobalRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	// An account row that is on must not leak into the account-less read.
	if _, err := service.Set(ctx, uuid.New(), uuid.New(), "owner", FlagSettings2FARequired, true); err != nil {
		t.Fatalf("account: %v", err)
	}
	required, err := service.Settings2FARequired(ctx, uuid.Nil)
	if err != nil || required {
		t.Fatalf("required = %v err = %v, want false", required, err)
	}
	if err := store.UpsertGlobal(ctx, FlagSettings2FARequired, true); err != nil {
		t.Fatalf("global: %v", err)
	}
	required, err = service.Settings2FARequired(ctx, uuid.Nil)
	if err != nil || !required {
		t.Fatalf("required = %v err = %v, want true", required, err)
	}
}

func TestSettings2_FA_RequiredRejectsNilService(t *testing.T) {
	t.Parallel()

	if _, err := (*Service)(nil).Settings2FARequired(context.Background(), uuid.New()); err == nil {
		t.Fatal("nil service")
	}
}
