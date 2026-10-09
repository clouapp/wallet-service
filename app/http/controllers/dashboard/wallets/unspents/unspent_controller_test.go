package unspents

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
)

func TestNew_UnspentController_RequiresTheUTXORecords(t *testing.T) {
	const want = "dashboard unspents controller: utxo service is required"
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v", got)
		}
	}()

	NewUnspentController(nil)

	t.Fatal("expected a panic")
}

func TestNew_UnspentController_KeepsTheUTXORecords(t *testing.T) {
	utxos := &walletrecords.UTXOs{}

	if NewUnspentController(utxos).utxos != utxos {
		t.Fatal("unspent controller did not keep the utxo service")
	}
}
