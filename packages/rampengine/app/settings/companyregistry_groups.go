package settings

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/companyregistry"
)

// The permission gate for the company registry groups, a pair of its own as
// the CEP lookup has: no credential sits behind it, but an operator who turns
// it off sends every Brazilian company to the manual form.
const (
	permCompanyRegistryView   = "company_registry.view"
	permCompanyRegistryUpdate = "company_registry.update"
)

// SectionCompanyRegistry is the settings page the CNPJ lookup shows on: the
// shared provider order plus one card per provider, as SectionPostalCodes.
const SectionCompanyRegistry = "company_registry"

// companyRegistryGroups declares the shared lookup-order group and one group
// per provider.
func companyRegistryGroups() []Group {
	groups := []Group{companyRegistryLookupGroup()}
	for _, code := range knownCompanyRegistryProviderCodes {
		groups = append(groups, companyRegistryProviderGroup(code))
	}
	return groups
}

func companyRegistryLookupGroup() Group {
	return Group{
		Name: companyregistry.LookupGroup,
		Settings: []Definition{
			{Key: companyregistry.SettingProviderOrder, Label: "Provider Order", Type: TypeStringList,
				Help:    "Providers are tried in this order until one answers or confirms the CNPJ is not registered. An empty list closes the lookup, and the app falls back to the manual form.",
				Options: knownCompanyRegistryProviderCodes,
				// The stored CSV shape, as postal's default: SettingsService
				// unboxes a default directly and only knows a string.
				Default: func() any { return companyregistry.ProviderBrasilApi }},
		},
		Section:          SectionCompanyRegistry,
		ViewPermission:   permCompanyRegistryView,
		UpdatePermission: permCompanyRegistryUpdate,
	}
}

func companyRegistryProviderGroup(code string) Group {
	return Group{
		Name: companyregistry.ProviderGroup(code),
		Settings: []Definition{
			{Key: companyregistry.SettingEnabled, Label: "Enabled", Type: TypeBool,
				Default: func() any { return true }},
		},
		Section:          SectionCompanyRegistry,
		ViewPermission:   permCompanyRegistryView,
		UpdatePermission: permCompanyRegistryUpdate,
	}
}

// knownCompanyRegistryProviderCodes is the closed vocabulary a lookup order
// may name.
var knownCompanyRegistryProviderCodes = []string{companyregistry.ProviderBrasilApi}
