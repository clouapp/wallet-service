package settings

import (
	"context"
	"errors"
	"strings"
)

// errPriceSettingsUnread is a price_lookup read that produced no order.
// The text never includes an api_key or its ciphertext.
var errPriceSettingsUnread = errors.New("price settings were not read")

// OpenedPriceProvider is one enabled price provider whose api_key was opened
// for this call. Callers must not log Key or its ciphertext.
type OpenedPriceProvider struct {
	Name string
	Key  string
}

// PriceProvidersForQuote returns enabled providers in provider_order. Each
// sealed api_key is opened now. A missing group, enabled false, an invalid
// seal, an unknown name, or a failed read of that provider is omitted. A
// failed read of price_lookup returns errPriceSettingsUnread so the quote
// can keep the environment CoinAPI key. Nothing here is logged.
func (s *Service) PriceProvidersForQuote(ctx context.Context) ([]OpenedPriceProvider, error) {
	if s == nil || ctx == nil || s.sealer == nil {
		return nil, errPriceSettingsUnread
	}
	stored, err := s.platformValues(ctx, groupPriceLookup)
	if err != nil {
		return nil, errPriceSettingsUnread
	}
	if len(stored) == 0 {
		return nil, nil
	}
	order := splitList(stored[keyProviderOrder])
	if len(order) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(order))
	out := make([]OpenedPriceProvider, 0, len(order))
	for _, name := range order {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		group, ok := priceGroupForProvider(name)
		if !ok {
			continue
		}
		key, ok := s.openedPriceAPIKey(ctx, group)
		if !ok {
			continue
		}
		out = append(out, OpenedPriceProvider{Name: name, Key: key})
	}
	return out, nil
}

func priceGroupForProvider(name string) (string, bool) {
	switch name {
	case priceProviderCoinGecko:
		return groupPriceCoinGecko, true
	case priceProviderCoinMarketCap:
		return groupPriceCoinMarketCap, true
	case priceProviderCoinAPI:
		return groupPriceCoinAPI, true
	default:
		return "", false
	}
}

// openedPriceAPIKey opens one price provider group. ok is false for a missing
// row, a disabled group, an invalid seal, a blank key, or any failed read.
// The store and sealer errors are dropped so they cannot carry the key.
func (s *Service) openedPriceAPIKey(ctx context.Context, group string) (string, bool) {
	if s == nil || s.sealer == nil {
		return "", false
	}
	stored, err := s.platformValues(ctx, group)
	if err != nil || len(stored) == 0 {
		return "", false
	}
	if strings.TrimSpace(stored[keyPriceEnabled]) != "true" {
		return "", false
	}
	sealed := stored[keyPriceAPIKey]
	if !IsSealed(sealed) {
		return "", false
	}
	opened, err := s.sealer.Open(sealed)
	if err != nil {
		return "", false
	}
	opened = strings.TrimSpace(opened)
	if opened == "" || IsSealed(opened) {
		return "", false
	}
	return opened, true
}
