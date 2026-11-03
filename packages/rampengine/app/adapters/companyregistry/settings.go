package companyregistry

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// CompanyRegistrySettingsSource is the narrow settings dependency this
// package reads, declared here so the package never depends on the settings
// service and app/settings can import it to declare the groups (the shape of
// postal.PostalSettingsSource, named apart so mockery writes them apart).
type CompanyRegistrySettingsSource interface {
	String(group, key string) (string, error)
	Bool(group, key string) (bool, error)
}

// LookupGroup is the settings group holding the provider order every CNPJ
// lookup shares.
const LookupGroup = "companyregistry_lookup"

// ProviderGroup is the settings group holding one provider's admin state.
func ProviderGroup(code ProviderCode) string { return "companyregistry_provider_" + code }

// The keys inside those groups.
const (
	SettingEnabled       = "enabled"
	SettingProviderOrder = "provider_order"
)

// ProviderState is one provider's admin-managed state.
type ProviderState struct {
	Code    ProviderCode
	Enabled bool
}

// Config reads the company lookup stack's admin state out of settings.
type Config struct{ settings CompanyRegistrySettingsSource }

func NewConfig(settings CompanyRegistrySettingsSource) *Config { return &Config{settings: settings} }

// Provider returns one provider's state. A provider nobody configured reads
// back as enabled, as a CEP provider does: it moves no money and holds no
// credential.
func (c *Config) Provider(code ProviderCode) (ProviderState, error) {
	enabled, err := c.settings.Bool(ProviderGroup(code), SettingEnabled)
	if err != nil {
		return ProviderState{}, fmt.Errorf("company registry config: %s enabled: %w", code, err)
	}
	return ProviderState{Code: code, Enabled: enabled}, nil
}

func (c *Config) Order() ([]ProviderCode, error) {
	raw, err := c.settings.String(LookupGroup, SettingProviderOrder)
	if err != nil {
		return nil, fmt.Errorf("company registry config: %s: %w", SettingProviderOrder, err)
	}
	return codes.ParseOrder(raw), nil
}
