package controllers

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletops"
)

func TestNew_AddressesHandler_KeepsItsOperations(t *testing.T) {
	ops := &walletops.Service{}

	handler := NewAddressesHandler("test", ops)

	if handler.ops != ops {
		t.Fatal("addresses handler did not keep the wallet operations")
	}
}

func TestNew_AddressesHandler_RequiresTheOperationsAndNamesTheSurface(t *testing.T) {
	const want = "test addresses controller: wallet operations are required"
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v", got)
		}
	}()

	NewAddressesHandler("test", nil)

	t.Fatal("expected a panic")
}
