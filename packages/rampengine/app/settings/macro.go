package settings

import "github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"

// RampGroups exposes only the payment provider catalog to Macro's existing
// settings service. The engine does not install a second settings API/store.
func RampGroups() []Group { return rampGroups() }

// ValidateRampGroup preserves Xip's validators while selecting the expected
// mode from the authenticated account environment instead of the host APP_ENV.
func ValidateRampGroup(group Group, values map[string]string, mode string) error {
	if len(group.Name) >= 9 && group.Name[:9] == "provider_" {
		if values[ramp.SettingEnabled] == "true" && values[ramp.SettingMode] != mode {
			return ramp.ValidateMode(values[ramp.SettingMode], map[string]string{"production": "production", "sandbox": "testing"}[mode])
		}
		if group.Name == ramp.ProviderGroup(ramp.ProviderBlindPay) {
			copy := make(map[string]string, len(values))
			for key, value := range values {
				copy[key] = value
			}
			copy[ramp.SettingEnabled] = "false"
			return validateBlindPayGroup(copy)
		}
		return nil
	}
	if group.Validate != nil {
		return group.Validate(values)
	}
	return nil
}
