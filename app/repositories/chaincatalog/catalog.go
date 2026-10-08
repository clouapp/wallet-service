// Package chaincatalog is the chain registry's reference data: the chains, their
// tokens, explorers and sweep thresholds. SeedChains and its siblings write it for
// a fresh database; SeedMissingAddedChains (chains:add-missing) tops up a live one.
package chaincatalog

import (
	"fmt"
	"strings"

	"github.com/spf13/cast"

	"github.com/macrowallets/waas/app/models"
)

const (
	placeholderRPCURL            = "https://placeholder.invalid"
	chainNetworkProfileConfigKey = "vault.chains.network_profile"
)

// Config is the part of the framework configuration the catalog reads.
type Config interface {
	GetString(path string, defaultValue ...string) string
	GetInt(path string, defaultValue ...int) int
	Env(envName string, defaultValue ...any) any
}

// Cipher seals the rpc_url column and opens the stored one.
type Cipher interface {
	EncryptString(value string) (string, error)
	DecryptString(payload string) (string, error)
}

// Catalog writes the reference data through the repositories, reading the
// environment from config and sealing endpoints with cipher.
type Catalog struct {
	config Config
	cipher Cipher
}

func New(config Config, cipher Cipher) *Catalog {
	return &Catalog{config: config, cipher: cipher}
}

// profile is CHAIN_NETWORK_PROFILE, "" when unset.
func (cat *Catalog) profile() (string, error) {
	profile := strings.TrimSpace(cat.config.GetString(chainNetworkProfileConfigKey))
	if profile != "" && !models.IsChainNetworkProfile(profile) {
		return "", fmt.Errorf("CHAIN_NETWORK_PROFILE=%q: want %q or %q",
			profile, models.ChainNetworkProfileMainnet, models.ChainNetworkProfileTestnet)
	}
	return profile, nil
}

// ChainIsTestnet is whether chainID points at a test network under the
// configured profile; without one, only the t-prefixed records do.
func (cat *Catalog) ChainIsTestnet(chainID string) (bool, error) {
	profile, err := cat.profile()
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

func (cat *Catalog) encryptRPCFromEnv(envKey string) (string, error) {
	raw := cast.ToString(cat.config.Env(envKey, ""))
	if raw == "" {
		raw = placeholderRPCURL
	}
	return cat.cipher.EncryptString(raw)
}

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }
