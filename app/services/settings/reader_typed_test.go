package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestString_Int_AndBoolRefuseASecretField(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailPassword: sealedPrefix + "stored-smtp-secret",
	})
	service := newTestService(store)
	ctx := context.Background()

	got, err := service.String(ctx, nil, groupMailSMTP, keyMailPassword)
	if !errors.Is(err, ErrSecretField) || got != "" {
		t.Fatal("string read of a secret returned a value")
	}
	n, err := service.Int(ctx, nil, groupMailSMTP, keyMailPassword)
	if !errors.Is(err, ErrSecretField) || n != 0 {
		t.Fatal("int read of a secret returned a value")
	}
	flag, err := service.Bool(ctx, nil, groupMailSMTP, keyMailPassword)
	if !errors.Is(err, ErrSecretField) || flag {
		t.Fatal("bool read of a secret returned a value")
	}
}

func TestSecret_Refuses_ANonSecretField(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailHost: "smtp.example"})
	service := newTestService(store)

	got, err := service.Secret(context.Background(), nil, groupMailSMTP, keyMailHost)
	if !errors.Is(err, ErrNotASecret) || got != "" {
		t.Fatal("secret read of a non-secret returned a value")
	}
}

func TestSecret_Unset_FieldUsesTheKnownEnvFallback(t *testing.T) {
	const fallback = "smtp-password-fallback"
	t.Setenv(envMailPassword, "  "+fallback+"  ")

	service := newTestService(newMemoryStore())
	ctx := context.Background()

	got, err := service.Secret(ctx, nil, groupMailSMTP, keyMailPassword)
	if err != nil || got != fallback {
		t.Fatal("unset secret did not use the env fallback")
	}

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailPassword: "   "})
	blank, err := newTestService(store).Secret(ctx, nil, groupMailSMTP, keyMailPassword)
	if err != nil || blank != fallback {
		t.Fatal("blank secret did not use the env fallback")
	}
}

func TestSecret_Unset_FieldWithoutAnEnvFallbackStaysUnset(t *testing.T) {
	t.Setenv("MAIL_SES_SECRET", "invented-ses-secret")
	t.Setenv(envMailPassword, "smtp-password-fallback")

	service := newTestService(newMemoryStore())
	got, err := service.Secret(context.Background(), nil, groupMailSES, keyMailProviderSecret)
	if err != nil || got != "" {
		t.Fatal("unset secret without an env fallback was given a value")
	}
}

func TestSecret_Bad_SealFailsClosed(t *testing.T) {
	t.Setenv(envMailPassword, "smtp-password-fallback")

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailPassword: sealedPrefix + "sealed-blob",
	})
	service := NewService(Deps{Store: store, Sealer: refuseOpenSealer{}, Cache: nopCache{}, Activity: discardActivity{}})

	got, err := service.Secret(context.Background(), nil, groupMailSMTP, keyMailPassword)
	if !errors.Is(err, ErrSecretSeal) || got != "" {
		t.Fatal("bad seal returned a secret")
	}

	unsealed := newMemoryStore()
	unsealed.PutPlatform(groupMailSMTP, map[string]string{keyMailPassword: "ciphertext-without-a-marker"})
	got, err = newTestService(unsealed).Secret(context.Background(), nil, groupMailSMTP, keyMailPassword)
	if !errors.Is(err, ErrSecretSeal) || got != "" {
		t.Fatal("unsealed secret was returned")
	}
}

func TestSecret_Opens_ASealedValueAndIgnoresTheEnvFallback(t *testing.T) {
	const opened = "stored-smtp-secret"
	t.Setenv(envMailPassword, "smtp-password-fallback")

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailPassword: sealedPrefix + opened})
	got, err := newTestService(store).Secret(context.Background(), nil, groupMailSMTP, keyMailPassword)
	if err != nil || got != opened {
		t.Fatal("sealed secret was not opened")
	}
}

func TestTyped_Readers_UseStoredNonSecretsOrTheRegistryDefault(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailHost: "smtp.example", keyMailPort: "2525"})
	store.PutPlatform(groupPriceCoinGecko, map[string]string{keyPriceEnabled: "true"})
	service := newTestService(store)
	ctx := context.Background()

	host, err := service.String(ctx, nil, groupMailSMTP, keyMailHost)
	if err != nil || host != "smtp.example" {
		t.Fatalf("host = %q, %v", host, err)
	}
	port, err := service.Int(ctx, nil, groupMailSMTP, keyMailPort)
	if err != nil || port != 2525 {
		t.Fatalf("port = %d, %v", port, err)
	}
	enabled, err := service.Bool(ctx, nil, groupPriceCoinGecko, keyPriceEnabled)
	if err != nil || !enabled {
		t.Fatalf("enabled = %v, %v", enabled, err)
	}

	missing := newTestService(newMemoryStore())
	defaultPort, err := missing.Int(ctx, nil, groupMailSMTP, keyMailPort)
	if err != nil || defaultPort != defaultMailPort {
		t.Fatalf("default port = %d, %v", defaultPort, err)
	}
	defaultEnabled, err := missing.Bool(ctx, nil, groupPriceCoinGecko, keyPriceEnabled)
	if err != nil || defaultEnabled {
		t.Fatalf("default enabled = %v, %v", defaultEnabled, err)
	}
}

func TestSecret_Opens_AnAccountSecretAndLeavesAnUnsetOneEmpty(t *testing.T) {
	t.Parallel()

	const opened = "account-signing-secret"
	accountID := uuid.New()
	store := newMemoryStore()
	if err := store.UpsertMany(context.Background(), accountID, groupAccountWebhooks, map[string]string{
		keySigningSecret: sealedPrefix + opened,
	}); err != nil {
		t.Fatal("store account secret")
	}
	service := newTestService(store)
	ctx := context.Background()

	got, err := service.Secret(ctx, &accountID, groupAccountWebhooks, keySigningSecret)
	if err != nil || got != opened {
		t.Fatal("account secret was not opened")
	}
	text, err := service.String(ctx, &accountID, groupAccountWebhooks, keySigningSecret)
	if !errors.Is(err, ErrSecretField) || text != "" {
		t.Fatal("string read of an account secret returned a value")
	}

	other := uuid.New()
	unset, err := service.Secret(ctx, &other, groupAccountWebhooks, keySigningSecret)
	if err != nil || unset != "" {
		t.Fatal("unset account secret was given a value")
	}
}
