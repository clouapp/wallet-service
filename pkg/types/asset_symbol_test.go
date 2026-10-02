package types

import "testing"

func TestCanonicalAssetSymbolMapsLegacyMaticToPol(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"matic":    NativeSymbolPOL,
		"MATIC":    NativeSymbolPOL,
		" Matic ":  NativeSymbolPOL,
		"pol":      "pol",
		"POL":      "POL",
		"USDC":     "USDC",
		"":         "",
		"maticx":   "maticx",
		"wmatic":   "wmatic",
		" eth ":    "eth",
	}
	for input, want := range cases {
		if got := CanonicalAssetSymbol(input); got != want {
			t.Errorf("CanonicalAssetSymbol(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSameAssetSymbolTreatsMaticAsPol(t *testing.T) {
	t.Parallel()

	same := [][2]string{{"matic", "pol"}, {"MATIC", "POL"}, {"pol", "POL"}, {"usdc", "USDC"}}
	for _, pair := range same {
		if !SameAssetSymbol(pair[0], pair[1]) {
			t.Errorf("SameAssetSymbol(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	different := [][2]string{{"matic", "eth"}, {"pol", "USDC"}, {"wmatic", "pol"}, {"", "pol"}}
	for _, pair := range different {
		if SameAssetSymbol(pair[0], pair[1]) {
			t.Errorf("SameAssetSymbol(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}
