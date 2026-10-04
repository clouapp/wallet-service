package settings

const (
	sectionProviders = "providers"

	blockWebhookProviders = "Webhooks"
	blockHeightProvider   = "Block height"

	keyProviderEnabled   = "enabled"
	keyProviderAPIKey    = "api_key"
	keyProviderAuthToken = "auth_token"
)

// S1.4.4 row: ALCHEMY_AUTH_TOKEN, HELIUS_API_KEY, QUICKNODE_API_KEY
// (vault.webhooks.*) move to platform settings. The groups are
// provider_alchemy, provider_helius, and provider_quicknode, with enabled
// and api_key/auth_token(Secret), section providers.
//
// ALCHEMY_AUTH_TOKEN is auth_token. HELIUS_API_KEY and QUICKNODE_API_KEY are
// api_key. Both secret fields are sealed by the settings sealer (enc:v1:)
// on PUT and omitted from the response. A blank secret keeps the stored one.
// The row's note is only the section name. It does not say the scanner or
// the RPC client reads these groups, so chain dialing stays on chains.rpc_url.
// A missing row is not consulted by scanning or by withdrawals.

func providerAlchemyGroup() Group {
	return webhookProviderGroup(groupProviderAlchemy, "Alchemy", keyProviderAuthToken, "Auth token")
}

func providerHeliusGroup() Group {
	return webhookProviderGroup(groupProviderHelius, "Helius", keyProviderAPIKey, "API key")
}

func providerQuickNodeGroup() Group {
	return webhookProviderGroup(groupProviderQuickNode, "QuickNode", keyProviderAPIKey, "API key")
}

func webhookProviderGroup(name, label, secretKey, secretLabel string) Group {
	return Group{
		Name:    name,
		Scope:   ScopePlatform,
		Section: sectionProviders,
		Block:   blockWebhookProviders,
		// The secret is sealed and omitted. This group names no permission:
		// a platform_admins row is the gate. enabled is returned.
		Settings: []Definition{
			{
				Key:     keyProviderEnabled,
				Label:   "Enabled",
				Help:    "Whether " + label + " is marked enabled.",
				Type:    TypeBool,
				Default: func() any { return false },
			},
			{
				Key:     secretKey,
				Label:   secretLabel,
				Help:    label + " credential. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
		},
	}
}

func webhookProviderGroupNames() []string {
	return []string{groupProviderAlchemy, groupProviderHelius, groupProviderQuickNode}
}

// S1.4.4 row: ETHERSCAN_API_KEY moves to platform settings. The group is
// provider_etherscan, with enabled and api_key(Secret). The note is
// "block-height provider". Height checks open this group at use time, when
// it is enabled and the sealed api_key opens. A missing row, enabled false,
// an invalid seal, or a failed read keeps vault.webhooks.etherscan_api_key.
// api_key is sealed by the settings sealer (enc:v1:) on PUT and omitted from
// the response and from activity. A blank key keeps the stored one.
func providerEtherscanGroup() Group {
	return Group{
		Name:    groupProviderEtherscan,
		Scope:   ScopePlatform,
		Section: sectionProviders,
		Block:   blockHeightProvider,
		// The key is sealed and omitted. This group names no permission:
		// a platform_admins row is the gate. enabled is returned.
		Settings: []Definition{
			{
				Key:     keyProviderEnabled,
				Label:   "Enabled",
				Help:    "Whether Etherscan is marked enabled.",
				Type:    TypeBool,
				Default: func() any { return false },
			},
			{
				Key:     keyProviderAPIKey,
				Label:   "API key",
				Help:    "Etherscan API key. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
		},
	}
}
