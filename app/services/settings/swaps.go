package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/swap"
)

const (
	GroupSwaps           = "swap_routing"
	GroupSwapKit         = "swap_provider_swapkit"
	GroupLiFi            = "swap_provider_lifi"
	keyPreferredProvider = "preferred_provider"
	keySwapFallback      = "fallback"
	keySwapSlippage      = "slippage_bps"
	keySwapTTL           = "quote_ttl_seconds"
	swapInherit          = "inherit"
)

func swapGroups() []Group {
	groups := []Group{
		{Name: GroupSwaps, Scope: ScopePlatform, Section: "swaps", Block: "Routing", Settings: []Definition{
			{Key: "enabled", Label: "Enable swaps", Help: "Allow new swap quotes and executions. Existing orders continue to be tracked when disabled.", Type: TypeBool, Default: func() any { return false }},
			{Key: keyPreferredProvider, Label: "Preferred provider", Help: "First provider used when a currency inherits the global preference.", Type: TypeString, Options: []string{swap.ProviderSwapKit, swap.ProviderLiFi}, Default: func() any { return swap.ProviderSwapKit }},
			{Key: keySwapFallback, Label: "Fallback", Help: "Try the other enabled provider when a quote has no route or its provider is unavailable. Accepted orders keep their provider.", Type: TypeBool, Default: func() any { return true }},
			{Key: keySwapSlippage, Label: "Slippage tolerance (bps)", Help: "Maximum price variation accepted in a quote. 100 basis points is 1%.", Type: TypeInt, Default: func() any { return 100 }},
			{Key: keySwapTTL, Label: "Quote validity (seconds)", Help: "Maximum lifetime of a quote. A provider may set a shorter lifetime.", Type: TypeInt, Default: func() any { return 60 }},
		}, Validate: validateSwapSettings},
	}
	for _, provider := range []struct{ name, label string }{{GroupSwapKit, "SwapKit"}, {GroupLiFi, "LI.FI"}} {
		group := Group{Name: provider.name, Scope: ScopePlatform, Section: "swaps", Block: "Providers",
			ViewPermission: policies.PermProvidersView, UpdatePermission: policies.PermProvidersUpdate,
			Settings: []Definition{
				{Key: "enabled", Label: "Enabled", Help: "Use " + provider.label + " for new quotes and executions.", Type: TypeBool, Default: func() any { return false }},
				{Key: "api_key", Label: "API key", Help: provider.label + " credential. Blank keeps the stored key. LI.FI also supports keyless access at lower rate limits.", Type: TypeString, Secret: true, Default: func() any { return "" }},
			},
		}
		groups = append(groups, group)
	}
	for _, symbol := range []string{"BTC", "ETH", "SOL", "LTC", "XRP", "BNB", "TRX", "POL", "USDT", "USDC"} {
		groups = append(groups, swapRuleGroup(symbol, ""))
	}
	for _, symbol := range []string{"USDT", "USDC"} {
		for _, chain := range []string{"eth", "polygon", "sol", "base", "arbitrum", "bsc", "tron"} {
			if symbol == "USDC" && chain == "tron" {
				continue
			}
			groups = append(groups, swapRuleGroup(symbol, chain))
		}
	}
	return groups
}

func swapRuleGroup(symbol, chain string) Group {
	name := "swap_" + strings.ToLower(symbol)
	block := "Currency preferences"
	if chain != "" {
		name += "_" + chain
		block = "Network overrides"
	}
	return Group{Name: name, Scope: ScopePlatform, Section: "swaps", Block: block, Settings: []Definition{
		{Key: keyPreferredProvider, Label: "Preferred provider", Help: "Preference for swaps selling " + symbol + ". Inherit uses the currency preference, then the global preference.", Type: TypeString, Options: []string{swapInherit, swap.ProviderSwapKit, swap.ProviderLiFi}, Default: func() any { return swapInherit }},
		{Key: keySwapFallback, Label: "Fallback", Help: "Inherit uses the currency preference, then the global fallback policy.", Type: TypeString, Options: []string{swapInherit, "enabled", "disabled"}, Default: func() any { return swapInherit }},
	}}
}

func validateSwapSettings(values map[string]string) error {
	fields := map[string][]string{}
	slippage, err := strconv.Atoi(values[keySwapSlippage])
	if err != nil || slippage < 0 || slippage > 500 {
		fields[keySwapSlippage] = []string{"must be between 0 and 500"}
	}
	ttl, err := strconv.Atoi(values[keySwapTTL])
	if err != nil || ttl < 15 || ttl > 300 {
		fields[keySwapTTL] = []string{"must be between 15 and 300"}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// SwapRouting returns the current source-network, currency and global routing policy.
func (s *Service) SwapRouting(ctx context.Context, symbol, chain string) (swap.RoutingConfig, error) {
	var result swap.RoutingConfig
	if !swap.SupportedSymbol(symbol) {
		return result, swap.ErrUnsupported
	}
	var err error
	if result.Enabled, err = s.Bool(ctx, nil, GroupSwaps, "enabled"); err != nil {
		return result, err
	}
	if result.Preferred, err = s.String(ctx, nil, GroupSwaps, keyPreferredProvider); err != nil {
		return result, err
	}
	if result.Fallback, err = s.Bool(ctx, nil, GroupSwaps, keySwapFallback); err != nil {
		return result, err
	}
	if result.SlippageBPS, err = s.Int(ctx, nil, GroupSwaps, keySwapSlippage); err != nil {
		return result, err
	}
	ttl, err := s.Int(ctx, nil, GroupSwaps, keySwapTTL)
	if err != nil {
		return result, err
	}
	result.QuoteTTL = time.Duration(ttl) * time.Second
	base := "swap_" + strings.ToLower(symbol)
	for _, name := range []string{base, base + "_" + chain} {
		if _, exists := FindGroup(name); !exists {
			continue
		}
		preferred, readErr := s.String(ctx, nil, name, keyPreferredProvider)
		if readErr != nil {
			return result, readErr
		}
		fallback, readErr := s.String(ctx, nil, name, keySwapFallback)
		if readErr != nil {
			return result, readErr
		}
		if preferred != swapInherit {
			result.Preferred = preferred
		}
		if fallback != swapInherit {
			result.Fallback = fallback == "enabled"
		}
	}
	if result.Preferred != swap.ProviderSwapKit && result.Preferred != swap.ProviderLiFi {
		return result, fmt.Errorf("invalid swap provider configuration")
	}
	result.Providers = map[string]swap.ProviderConfig{}
	for _, provider := range []string{swap.ProviderSwapKit, swap.ProviderLiFi} {
		enabled, readErr := s.Bool(ctx, nil, "swap_provider_"+provider, "enabled")
		if readErr != nil {
			return result, readErr
		}
		if !enabled {
			result.Providers[provider] = swap.ProviderConfig{}
			continue
		}
		credential, readErr := s.SwapProvider(ctx, provider)
		if readErr != nil {
			return result, readErr
		}
		result.Providers[provider] = credential
	}
	return result, nil
}

// SwapProvider opens a credential for one call, including tracking of disabled providers.
func (s *Service) SwapProvider(ctx context.Context, name string) (swap.ProviderConfig, error) {
	group := "swap_provider_" + name
	if name != swap.ProviderSwapKit && name != swap.ProviderLiFi {
		return swap.ProviderConfig{}, swap.ErrUnsupported
	}
	enabled, err := s.Bool(ctx, nil, group, "enabled")
	if err != nil {
		return swap.ProviderConfig{}, err
	}
	key, err := s.Secret(ctx, nil, group, "api_key")
	if err != nil {
		return swap.ProviderConfig{}, err
	}
	if name == swap.ProviderSwapKit && strings.TrimSpace(key) == "" {
		enabled = false
	}
	return swap.ProviderConfig{Enabled: enabled, APIKey: key}, nil
}
