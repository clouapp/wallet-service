package main

import (
	"math/big"
	"testing"
)

func TestParseEther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "whole ether", input: "1", want: "1000000000000000000"},
		{name: "fractional ether", input: "0.02", want: "20000000000000000"},
		{name: "one wei", input: "0.000000000000000001", want: "1"},
		{name: "zero", input: "0", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
		{name: "too many decimals", input: "0.0000000000000000001", wantErr: true},
		{name: "not a number", input: "abc", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseEther(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseEther(%q) expected error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEther(%q): %v", test.input, err)
			}
			if got.Cmp(mustBigInt(t, test.want)) != 0 {
				t.Fatalf("parseEther(%q) = %s, want %s", test.input, got, test.want)
			}
		})
	}
}

func TestParsePrivateKeyRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "0x", "not-hex", "01"} {
		if _, err := parsePrivateKey(input); err == nil {
			t.Fatalf("parsePrivateKey(%q) expected error", input)
		}
	}
}

func TestParsePrivateKeyAcceptsHexPrefix(t *testing.T) {
	t.Parallel()

	const key = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412e2f9f60f4f6f0e"
	privateKey, err := parsePrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if privateKey.D.Sign() <= 0 {
		t.Fatal("private key scalar must be positive")
	}
}

func TestBufferedGasPriceDoublesSuggestion(t *testing.T) {
	t.Parallel()

	got := bufferedGasPrice(big.NewInt(1_500_000_000))
	if got.String() != "3000000000" {
		t.Fatalf("bufferedGasPrice = %s", got)
	}
}

func TestBufferedGasPriceDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	input := big.NewInt(1_500_000_000)
	_ = bufferedGasPrice(input)
	if input.String() != "1500000000" {
		t.Fatalf("input was mutated to %s", input)
	}
}

func TestDynamicFeeCaps(t *testing.T) {
	t.Parallel()

	baseFee := big.NewInt(1_500_000_000)
	tip := big.NewInt(2_000_000)
	feeCap, tipCap, err := dynamicFeeCaps(baseFee, tip)
	if err != nil {
		t.Fatal(err)
	}
	if feeCap.String() != "3002000000" {
		t.Fatalf("fee cap = %s", feeCap)
	}
	if tipCap.String() != "2000000" {
		t.Fatalf("tip cap = %s", tipCap)
	}
	if baseFee.String() != "1500000000" || tip.String() != "2000000" {
		t.Fatal("dynamicFeeCaps mutated its inputs")
	}
}

func TestDynamicFeeCapsRejectsMissingFees(t *testing.T) {
	t.Parallel()

	if _, _, err := dynamicFeeCaps(nil, big.NewInt(1)); err == nil {
		t.Fatal("expected nil base fee error")
	}
	if _, _, err := dynamicFeeCaps(big.NewInt(1), nil); err == nil {
		t.Fatal("expected nil tip error")
	}
}

func mustBigInt(t *testing.T, value string) *big.Int {
	t.Helper()
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		t.Fatalf("invalid test big.Int %q", value)
	}
	return result
}
