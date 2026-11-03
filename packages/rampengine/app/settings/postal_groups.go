package settings

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/postal"
)

// The permission gate for the postal lookup groups. Read-only providers with
// no credential still get their own permission rather than folding into
// settings.* — consistent with how the ramp provider stack is gated, and
// cheap to grant broadly since there is nothing secret behind it.
const (
	permPostalCodesView   = "postal_codes.view"
	permPostalCodesUpdate = "postal_codes.update"
)

// SectionPostalCodes is the single settings page the postal lookup stack
// shows on: the shared provider order plus one card per provider.
const SectionPostalCodes = "postal_codes"

// postalGroups declares the shared lookup-order group and one group per
// provider.
func postalGroups() []Group {
	groups := []Group{lookupGroup()}
	for _, code := range knownPostalProviderCodes {
		groups = append(groups, postalProviderGroup(code))
	}
	return groups
}

func lookupGroup() Group {
	return Group{
		Name: postal.LookupGroup,
		Settings: []Definition{
			{Key: postal.SettingProviderOrder, Label: "Provider Order", Type: TypeStringList,
				Help:    "Providers are tried in this order until one answers or confirms the postal code does not exist. An empty list closes the lookup.",
				Options: knownPostalProviderCodes,
				// The default is the stored CSV shape, not a []string: unlike
				// GetGroup (which round-trips through castIn/castOut),
				// SettingsService.String() unboxes Default() directly and
				// only knows how to do that for a string — see
				// ramp.SettingProviderOrder's default for the same rule.
				Default: func() any { return postal.ProviderViaCep + "," + postal.ProviderBrasilApi }},
		},
		Section:          SectionPostalCodes,
		ViewPermission:   permPostalCodesView,
		UpdatePermission: permPostalCodesUpdate,
	}
}

func postalProviderGroup(code string) Group {
	return Group{
		Name: postal.ProviderGroup(code),
		Settings: []Definition{
			{Key: postal.SettingEnabled, Label: "Enabled", Type: TypeBool,
				Default: func() any { return true }},
		},
		Section:          SectionPostalCodes,
		ViewPermission:   permPostalCodesView,
		UpdatePermission: permPostalCodesUpdate,
	}
}

// knownPostalProviderCodes is the closed vocabulary a lookup order may name.
var knownPostalProviderCodes = []string{
	postal.ProviderViaCep,
	postal.ProviderBrasilApi,
}
