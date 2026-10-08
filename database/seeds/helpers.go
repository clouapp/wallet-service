package seeds

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/facades"
	"github.com/spf13/cast"

	"github.com/macrowallets/waas/app/models"
)

const (
	placeholderRPCURL            = "https://placeholder.invalid"
	chainNetworkProfileConfigKey = "vault.chains.network_profile"
)

// configuredChainNetworkProfile is CHAIN_NETWORK_PROFILE, "" when unset.
func configuredChainNetworkProfile() (string, error) {
	profile := strings.TrimSpace(facades.Config().GetString(chainNetworkProfileConfigKey))
	if profile != "" && !models.IsChainNetworkProfile(profile) {
		return "", fmt.Errorf("CHAIN_NETWORK_PROFILE=%q: want %q or %q",
			profile, models.ChainNetworkProfileMainnet, models.ChainNetworkProfileTestnet)
	}
	return profile, nil
}

// seedChainIsTestnet is whether chainID points at a test network under the
// configured profile; without one, only the t-prefixed records do.
func seedChainIsTestnet(chainID string) (bool, error) {
	profile, err := configuredChainNetworkProfile()
	if err != nil {
		return false, err
	}
	if profile == "" {
		profile = models.ChainNetworkProfileMainnet
	}
	spec, decided, err := models.PrimaryChainNetwork(profile, chainID)
	if err != nil {
		return false, err
	}
	if decided {
		return spec.IsTestnet, nil
	}
	return models.IsTestChainID(chainID), nil
}

func encryptRPCFromEnv(envKey string) (string, error) {
	raw := cast.ToString(facades.Config().Env(envKey, ""))
	if raw == "" {
		raw = placeholderRPCURL
	}
	return facades.Crypt().EncryptString(raw)
}

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }
