package types

import "strings"

const (
	// NativeSymbolPOL is the Polygon native asset (POL replaced MATIC in 2024).
	NativeSymbolPOL = "pol"
	// LegacyNativeSymbolMATIC is still accepted as input for POL.
	LegacyNativeSymbolMATIC = "matic"
)

// CanonicalAssetSymbol trims symbol and maps the legacy MATIC ticker to POL;
// every other symbol is returned as given.
func CanonicalAssetSymbol(symbol string) string {
	trimmed := strings.TrimSpace(symbol)
	if strings.EqualFold(trimmed, LegacyNativeSymbolMATIC) {
		return NativeSymbolPOL
	}
	return trimmed
}

// SameAssetSymbol compares symbols case-insensitively, treating MATIC as POL.
func SameAssetSymbol(a, b string) bool {
	return strings.EqualFold(CanonicalAssetSymbol(a), CanonicalAssetSymbol(b))
}
