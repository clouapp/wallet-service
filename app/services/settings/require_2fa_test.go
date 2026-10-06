package settings

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRequire2_FA_MissingRowIsDefaultFalse(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()

	required, err := service.Require2FA(context.Background(), accountID)
	if err != nil {
		t.Fatalf("require 2fa: %v", err)
	}
	if required {
		t.Fatal("missing require_2fa must use the registry default false")
	}
	if _, ok := store.get(accountID, groupAccountSecurity, keyRequire2FA); ok {
		t.Fatal("read inserted a row")
	}
}

func TestRequire2_FA_StoredValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "blank", value: "  ", want: false},
		{name: "invalid falls back", value: "yes", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemoryStore()
			service := newTestService(store)
			accountID := uuid.New()
			if err := store.UpsertMany(ctx, accountID, groupAccountSecurity, map[string]string{
				keyRequire2FA:         tc.value,
				keySessionIdleMinutes: "15",
			}); err != nil {
				t.Fatalf("store: %v", err)
			}
			if err := store.UpsertMany(ctx, accountID, groupAccountWebhooks, map[string]string{
				keyDefaultEvents: "deposit.confirmed",
			}); err != nil {
				t.Fatalf("webhooks: %v", err)
			}

			required, err := service.Require2FA(ctx, accountID)
			if err != nil {
				t.Fatalf("require 2fa: %v", err)
			}
			if required != tc.want {
				t.Fatalf("require 2fa = %v, want %v", required, tc.want)
			}
			idle, ok := store.get(accountID, groupAccountSecurity, keySessionIdleMinutes)
			if !ok || idle != "15" {
				t.Fatalf("session idle changed: present %v value %q", ok, idle)
			}
		})
	}
}

func TestRequire2_FA_IgnoresSessionIdleAndWebhooksWhenRequire2FAIsAbsent(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if err := store.UpsertMany(ctx, accountID, groupAccountSecurity, map[string]string{
		keySessionIdleMinutes: "15",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.UpsertMany(ctx, accountID, groupAccountWebhooks, map[string]string{
		keySigningAlgorithm: signingAlgorithmHMACSHA256,
	}); err != nil {
		t.Fatalf("webhooks: %v", err)
	}

	required, err := service.Require2FA(ctx, accountID)
	if err != nil {
		t.Fatalf("require 2fa: %v", err)
	}
	if required {
		t.Fatal("session idle and account webhooks must not require 2FA")
	}
}

func TestRequire2_FA_RejectsNilAccount(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	if _, err := service.Require2FA(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil account")
	}
	if _, err := (*Service)(nil).Require2FA(context.Background(), uuid.New()); err == nil {
		t.Fatal("nil service")
	}
}
