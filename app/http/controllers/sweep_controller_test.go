package controllers

import (
	"math/big"
	"testing"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/sweep"
)

func TestConsolidateResponseTotalsTheSweptLegs(t *testing.T) {
	decimals := 6
	result := &sweep.Result{
		Sweeps: []sweep.CompletedSweep{
			{From: models.Address{Address: "TChildA"}, TxHash: "a1", Amount: big.NewInt(7_000_000)},
			{From: models.Address{Address: "TChildB"}, TxHash: "b2", Amount: big.NewInt(2_500_001)},
		},
		AssetDecimals: &decimals,
	}

	body := mapConsolidateResponse(result)
	summary := body["plan_summary"].(http.Json)
	if summary["total_amount"] != "9500001" || summary["children_swept"] != 2 || summary["decimals"] != 6 {
		t.Fatalf("summary %v", summary)
	}
	txs := body["transactions"].([]http.Json)
	if txs[0]["amount"] != "7000000" || txs[1]["amount"] != "2500001" {
		t.Fatalf("transactions %v", txs)
	}
}

func TestConsolidateResponseWithoutLegsTotalsZero(t *testing.T) {
	summary := mapConsolidateResponse(&sweep.Result{})["plan_summary"].(http.Json)
	if summary["total_amount"] != "0" {
		t.Fatalf("total %v", summary["total_amount"])
	}
	if _, ok := summary["decimals"]; ok {
		t.Fatalf("decimals reported without an asset: %v", summary)
	}
}
