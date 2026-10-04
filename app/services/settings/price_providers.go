package settings

import "github.com/macrowallets/waas/app/policies"

const (
	sectionPrice = "price"

	blockPriceLookup    = "Lookup"
	blockPriceProviders = "Providers"

	keyProviderOrder = "provider_order"
	keyPriceEnabled  = "enabled"
	keyPriceAPIKey   = "api_key"

	priceProviderCoinGecko     = "coingecko"
	priceProviderCoinMarketCap = "coinmarketcap"
	priceProviderCoinAPI       = "coinapi"
)

// S1.4.4 registers price_lookup.provider_order and price_coingecko,
// price_coinmarketcap, and price_coinapi. Quotes read them through
// price.SettingsSource on each refresh: key, enabled, and order are resolved
// then, and each sealed api_key is opened at that moment. A missing group,
// enabled false, an invalid seal, an unknown name, or a failed read skips
// that provider. When none are usable, the quote keeps the environment
// CoinAPI key. Base URLs stay adapter constants: these groups have no
// base_url field.

func priceLookupGroup() Group {
	return Group{
		Name:    groupPriceLookup,
		Scope:   ScopePlatform,
		Section: sectionPrice,
		Block:   blockPriceLookup,
		// provider_order is not a secret. S1.4.7 names providers.view and
		// providers.update for price provider keys, not this order. A
		// platform_admins row is the gate.
		Settings: []Definition{
			{
				Key:     keyProviderOrder,
				Label:   "Provider order",
				Help:    "Price providers, in order. Each name is coingecko, coinmarketcap, or coinapi.",
				Type:    TypeStringList,
				Options: priceProviderNames(),
				Default: func() any { return []string{} },
			},
		},
	}
}

func priceCoinGeckoGroup() Group {
	return priceProviderGroup(groupPriceCoinGecko, "CoinGecko")
}

func priceCoinMarketCapGroup() Group {
	return priceProviderGroup(groupPriceCoinMarketCap, "CoinMarketCap")
}

func priceCoinAPIGroup() Group {
	return priceProviderGroup(groupPriceCoinAPI, "CoinAPI")
}

func priceProviderGroup(name, label string) Group {
	return Group{
		Name:    name,
		Scope:   ScopePlatform,
		Section: sectionPrice,
		Block:   blockPriceProviders,
		// api_key is a Secret. S1.4.7 names providers.view and
		// providers.update on this price provider key so settings.update is
		// not the grant. No account role holds that pair. There is no
		// platform permission catalog, so a platform_admins row stands in.
		// enabled is returned.
		ViewPermission:   policies.PermProvidersView,
		UpdatePermission: policies.PermProvidersUpdate,
		Settings: []Definition{
			{
				Key:     keyPriceEnabled,
				Label:   "Enabled",
				Help:    "Whether " + label + " is marked enabled.",
				Type:    TypeBool,
				Default: func() any { return false },
			},
			{
				Key:     keyPriceAPIKey,
				Label:   "API key",
				Help:    label + " API key. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
		},
	}
}

func priceProviderNames() []string {
	return []string{priceProviderCoinGecko, priceProviderCoinMarketCap, priceProviderCoinAPI}
}

func priceProviderGroupNames() []string {
	return []string{groupPriceCoinGecko, groupPriceCoinMarketCap, groupPriceCoinAPI}
}
