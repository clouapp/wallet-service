package feeestimate

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/services/sweep"
)

func TestBuildDetailsTron(t *testing.T) {
	details := buildDetails(&sweep.FeeQuote{Tron: &sweep.TronFeeDetails{
		BandwidthBytes: 345, SunPerBandwidthByte: 1_000, BandwidthFee: big.NewInt(345_000),
		ActivationFee: big.NewInt(0), Energy: 21_975, SunPerEnergy: 100, EnergyFeeLimit: big.NewInt(2_637_000),
	}})
	if details.EVM != nil || details.Bitcoin != nil || details.Solana != nil || details.Tron == nil {
		t.Fatalf("details %+v", details)
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tron":{"bandwidth_bytes":345,"sun_per_bandwidth_byte":1000,"bandwidth_fee_sun":"345000","account_activation_fee_sun":"0","energy":21975,"sun_per_energy":100,"energy_fee_limit_sun":"2637000","energy_is_reference":false}}`
	if string(encoded) != want {
		t.Fatalf("json\n got %s\nwant %s", encoded, want)
	}
}
