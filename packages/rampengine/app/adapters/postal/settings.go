package postal

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// PostalSettingsSource is the narrow settings dependency this package reads.
//
// It is declared here rather than imported so this package never depends on
// the settings service, and so `app/settings` can import this package to
// declare the provider groups without creating a cycle — the same shape as
// ramp.SettingsSource. It is not named SettingsSource: ramp already owns that
// name, and mockery would write both mocks to the same file (see the note on
// ramp.SettingsSource).
type PostalSettingsSource interface {
	String(group, key string) (string, error)
	Bool(group, key string) (bool, error)
}

// LookupGroup is the settings group holding the provider order shared by
// every CEP lookup, independent of which provider ends up answering.
const LookupGroup = "postal_lookup"

// ProviderGroup is the settings group holding one provider's admin state.
func ProviderGroup(code ProviderCode) string { return "postal_provider_" + code }

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

// Config reads the postal lookup stack's admin state out of settings.
type Config struct{ settings PostalSettingsSource }

func NewConfig(settings PostalSettingsSource) *Config { return &Config{settings: settings} }

// Provider returns one provider's state. A provider nobody has configured
// reads back as enabled — unlike a payment provider, an unconfigured CEP
// lookup provider is a safe default because it moves no money and holds no
// credential.
func (c *Config) Provider(code ProviderCode) (ProviderState, error) {
	enabled, err := c.settings.Bool(ProviderGroup(code), SettingEnabled)
	if err != nil {
		return ProviderState{}, fmt.Errorf("postal config: %s enabled: %w", code, err)
	}
	return ProviderState{Code: code, Enabled: enabled}, nil
}

func (c *Config) Order() ([]ProviderCode, error) {
	raw, err := c.settings.String(LookupGroup, SettingProviderOrder)
	if err != nil {
		return nil, fmt.Errorf("postal config: %s: %w", SettingProviderOrder, err)
	}
	return codes.ParseOrder(raw), nil
}
