package features

import "context"

const (
	FlagRampIntegrations       = "ramp-integrations-enabled"
	FlagRampOwnOperations      = "ramp-own-operations-enabled"
	FlagRampCustomerOperations = "ramp-customer-operations-enabled"
)

func init() {
	for _, entry := range []struct {
		key, label string
		enabled    bool
		globalOnly bool
	}{
		{FlagRampIntegrations, "Ramp integrations", false, false},
		{"ramp-payin-enabled", "Pix onramp", true, false},
		{"ramp-payout-enabled", "Pix offramp", true, false},
		{"ramp-usd-payin-enabled", "USD onramp", true, false},
		{"ramp-usd-payout-enabled", "USD offramp", true, false},
		{"ramp-bank-payout-enabled", "US bank payouts", false, false},
		{"kyb", "Business verification", false, false},
		{"kyc-usa-residence-enabled", "KYC for US residents", false, true},
		{FlagRampOwnOperations, "Account holder ramp operations", true, false},
		{FlagRampCustomerOperations, "End customer ramp operations", true, false},
	} {
		scopes := []string{ScopeGlobal, ScopeAccount}
		if entry.globalOnly {
			scopes = []string{ScopeGlobal}
		}
		catalog = append(catalog, Definition{Key: entry.key, Label: entry.label, Description: entry.label, Default: entry.enabled, Scopes: scopes})
	}
}

// GlobalEnabled also respects a closed catalog default, unlike the explicit
// global veto used for an account's established money-moving features.
func (s *Service) GlobalEnabled(ctx context.Context, key string) (bool, error) {
	definition, ok := Find(key)
	if !ok {
		return false, ErrNotFound
	}
	value, found, err := s.store.GetGlobal(ctx, key)
	if err != nil {
		return false, err
	}
	if found {
		return value, nil
	}
	return definition.Default, nil
}
