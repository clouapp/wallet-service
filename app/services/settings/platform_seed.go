package settings

import (
	"fmt"
	"strings"
)

// S1.4.3: PlatformSettingsSeed per group (mail_*, price_*, providers_*),
// values sealed in bootstrap, blanks skipped, ON CONFLICT DO NOTHING.
//
// The registry spells the provider groups provider_*. providers_* is accepted
// too. Only groups Registry already declares are visited. A field is copied
// only when platformSeedEnv names the variable the runtime already reads for
// that field. Feature flags are not settings rows and are not seeded.

const (
	envMailHost         = "MAIL_HOST"
	envMailPort         = "MAIL_PORT"
	envMailEncryption   = "MAIL_ENCRYPTION"
	envMailUsername     = "MAIL_USERNAME"
	envMailPassword     = "MAIL_PASSWORD"
	envMailFromAddress  = "MAIL_FROM_ADDRESS"
	envMailFromName     = "MAIL_FROM_NAME"
	envCoinGeckoAPIKey  = "COINGECKO_API_KEY"
	envCoinMarketCapKey = "COINMARKETCAP_API_KEY"
	envCoinAPIKey       = "COINAPI_KEY"
	envAlchemyAuthToken = "ALCHEMY_AUTH_TOKEN"
	envHeliusAPIKey     = "HELIUS_API_KEY"
	envQuickNodeAPIKey  = "QUICKNODE_API_KEY"
	envEtherscanAPIKey  = "ETHERSCAN_API_KEY"
)

// platformSeedEnv is the env var each seeded field already falls back to.
// mail SMTP and From: config/mail.go, kept by the mailer when the stored
// field is missing. Price keys: config/vault.go vault.price.*. Webhook and
// block-height keys: config/vault.go vault.webhooks.*, read by
// IngestProviderKey and EtherscanKeyForHeight. Fields with no entry are
// skipped, including enabled, driver, and provider_order.
var platformSeedEnv = map[string]string{
	groupMailSMTP + "\x00" + keyMailHost:                 envMailHost,
	groupMailSMTP + "\x00" + keyMailPort:                 envMailPort,
	groupMailSMTP + "\x00" + keyMailEncryption:           envMailEncryption,
	groupMailSMTP + "\x00" + keyMailUsername:             envMailUsername,
	groupMailSMTP + "\x00" + keyMailPassword:             envMailPassword,
	groupMailDelivery + "\x00" + keyMailFromAddress:      envMailFromAddress,
	groupMailDelivery + "\x00" + keyMailFromName:         envMailFromName,
	groupPriceCoinGecko + "\x00" + keyPriceAPIKey:        envCoinGeckoAPIKey,
	groupPriceCoinMarketCap + "\x00" + keyPriceAPIKey:    envCoinMarketCapKey,
	groupPriceCoinAPI + "\x00" + keyPriceAPIKey:          envCoinAPIKey,
	groupProviderAlchemy + "\x00" + keyProviderAuthToken: envAlchemyAuthToken,
	groupProviderHelius + "\x00" + keyProviderAPIKey:     envHeliusAPIKey,
	groupProviderQuickNode + "\x00" + keyProviderAPIKey:  envQuickNodeAPIKey,
	groupProviderEtherscan + "\x00" + keyProviderAPIKey:  envEtherscanAPIKey,
}

// PlatformSeedRow is one platform settings insert. Value is the text the
// registry stores. A secret Value is sealed (enc:v1:) and must not be logged.
type PlatformSeedRow struct {
	Group string
	Key   string
	Value string
}

// PlatformSettingsSeed copies non-blank env fallbacks for the platform groups
// S1.4.3 names. lookup reads one environment variable. sealer is the live
// settings sealer; it is required only when a secret fallback is non-blank.
// Nothing in the returned rows is written to the log by this function.
func PlatformSettingsSeed(lookup func(string) string, sealer Sealer) ([]PlatformSeedRow, error) {
	if lookup == nil {
		return nil, fmt.Errorf("platform settings seed: env lookup is required")
	}
	var rows []PlatformSeedRow
	for _, group := range Registry() {
		if group.Scope != ScopePlatform || !platformSeedGroup(group.Name) {
			continue
		}
		for _, definition := range group.Settings {
			envName, ok := platformSeedEnv[group.Name+"\x00"+definition.Key]
			if !ok {
				continue
			}
			raw := lookup(envName)
			if strings.TrimSpace(raw) == "" {
				continue
			}
			stored, err := platformSeedValue(definition, raw, sealer)
			if err != nil {
				return nil, fmt.Errorf("platform settings seed %s %s: %w", group.Name, definition.Key, err)
			}
			if stored == "" {
				continue
			}
			rows = append(rows, PlatformSeedRow{Group: group.Name, Key: definition.Key, Value: stored})
		}
	}
	return rows, nil
}

// PlatformSettingsSeedEnvNames lists the environment variables the seed reads.
// The names are not secret. Values are not returned.
func PlatformSettingsSeedEnvNames() []string {
	seen := map[string]struct{}{}
	names := make([]string, 0, len(platformSeedEnv))
	for _, name := range platformSeedEnv {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func platformSeedGroup(name string) bool {
	return strings.HasPrefix(name, "mail_") ||
		strings.HasPrefix(name, "price_") ||
		strings.HasPrefix(name, "providers_") ||
		strings.HasPrefix(name, "provider_")
}

func platformSeedValue(definition Definition, raw string, sealer Sealer) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if definition.Secret {
		if sealer == nil {
			return "", fmt.Errorf("sealer is required")
		}
		sealed, err := sealer.Seal(trimmed)
		if err != nil || sealed == "" || !IsSealed(sealed) {
			return "", fmt.Errorf("seal failed")
		}
		return sealed, nil
	}
	stored, err := castIn(trimmed, definition)
	if err != nil || stored == "" {
		return "", fmt.Errorf("value is not storable")
	}
	return stored, nil
}
