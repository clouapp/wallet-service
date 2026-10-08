package controllers

import (
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/withdraw"
)

func TestHuman_To_BaseUnits(t *testing.T) {
	got, err := withdraw.ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "0.001", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseUnits.String() != "1000000000000000" {
		t.Fatalf("got %s", got.BaseUnits.String())
	}

	got, err = withdraw.ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseUnits.String() != "1000000000000000000" {
		t.Fatalf("got %s", got.BaseUnits.String())
	}

	if _, err := withdraw.ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "0", nil); err == nil {
		t.Fatal("expected error for zero")
	}
	if _, err := withdraw.ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "1.1234567890123456789", nil); err == nil {
		t.Fatal("expected error for extra decimals")
	}
}

func TestWithdrawal_ID_FromIdempotencyKey(t *testing.T) {
	t.Parallel()

	key := uuid.New()
	got, err := withdrawalIDFromIdempotencyKey(key.String())
	if err != nil {
		t.Fatal(err)
	}
	if got != key {
		t.Fatalf("got %s, want %s", got, key)
	}
}

func TestWithdrawal_ID_FromIdempotencyKeyGeneratesIDWhenAbsent(t *testing.T) {
	t.Parallel()

	got, err := withdrawalIDFromIdempotencyKey("")
	if err != nil {
		t.Fatal(err)
	}
	if got == uuid.Nil {
		t.Fatal("expected a generated withdrawal id")
	}
}

func TestWithdrawal_ID_FromIdempotencyKeyRejectsInvalidUUID(t *testing.T) {
	t.Parallel()

	if _, err := withdrawalIDFromIdempotencyKey("not-a-uuid"); err == nil {
		t.Fatal("expected invalid idempotency key error")
	}
}
