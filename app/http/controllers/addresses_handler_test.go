package controllers

import (
	"testing"

	deposit "github.com/macrowallets/waas/app/services/deposit"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func addressesHandlerDeps() AddressesHandlerDeps {
	svc := &wallet.Service{}
	return AddressesHandlerDeps{
		Addresses:     &walletrecords.Addresses{},
		WalletService: func() *wallet.Service { return svc },
		Deposits:      &deposit.Service{},
	}
}

func TestNew_AddressesHandler_KeepsItsDependencies(t *testing.T) {
	deps := addressesHandlerDeps()
	ctrl := NewAddressesHandler("test", deps)
	if ctrl == nil {
		t.Fatal("NewAddressesHandler returned nil")
	}
	if ctrl.addresses != deps.Addresses {
		t.Fatal("addresses controller did not keep the addresses service")
	}
	if ctrl.walletService == nil || ctrl.walletService() != deps.WalletService() {
		t.Fatal("addresses controller did not keep the wallet service")
	}
	if ctrl.deposits != deps.Deposits {
		t.Fatal("addresses controller did not keep the deposit service")
	}
}
