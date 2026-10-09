package unspents_test

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/dashboard/wallets/unspents"
	"github.com/macrowallets/waas/app/models"
)

func TestUnspentOutputs(t *testing.T) {
	block := int64(840000)
	outputs := unspents.UnspentOutputs([]models.WalletUTXO{
		{TxHash: "aa", OutputIndex: 1, ValueRaw: "5000", BlockNumber: &block, Address: "bc1qa"},
		{TxHash: "bb", OutputIndex: 0, ValueRaw: "not a number", Address: "bc1qb"},
	})

	raw, err := json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"tx_hash":"aa","vout":1,"value":5000,"height":840000,"address":"bc1qa"},{"tx_hash":"bb","vout":0,"value":0,"height":0,"address":"bc1qb"}]`
	if string(raw) != want {
		t.Fatalf("wire changed\n got %s\nwant %s", raw, want)
	}
}

func TestUnspentOutputs_NoneIsAnEmptyList(t *testing.T) {
	raw, err := json.Marshal(unspents.UnspentOutputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("outputs = %s, want []", raw)
	}
}
