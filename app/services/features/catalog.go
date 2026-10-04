// Package features is the feature-flag catalog and the service that reads and
// writes it. A flag is a named switch. Metadata lives here. The features
// table stores the boolean an owner or admin wrote for one account. The
// global_features table stores the boolean a platform admin wrote for every
// account. A missing row uses the catalog default and is not inserted. The
// money-moving gate treats an explicit global false as a veto; a missing or
// true global row does not block. Nothing is cached.
package features

import "slices"

const (
	// ScopeAccount is the per-account switch. The account path does not take
	// a scope segment.
	ScopeAccount = "account"
	// ScopeGlobal is the platform switch. It uses the same key and the same
	// default as the account scope.
	ScopeGlobal = "global"
)

// Named account flags. The strings are the API keys and the features.key
// column. Keep the list complete; All sorts it for the response.
const (
	FlagAPIRequestSignatureRequired = "api-request-signature-required"
	FlagDepositScanEnabled          = "deposit-scan-enabled"
	FlagSweepEnabled                = "sweep-enabled"
	FlagUser2FARequired             = "user-2fa-required"
	FlagWalletCreationEnabled       = "wallet-creation-enabled"
	FlagWebhookDeliveryEnabled      = "webhook-delivery-enabled"
	FlagWithdrawalsEnabled          = "withdrawals-enabled"
)

// Definition is one named flag. Default is what a reader returns when the
// account has no row. withdrawals-enabled, sweep-enabled, and
// deposit-scan-enabled default to true, so a missing row leaves withdrawals
// and consolidate running and allows scans. The other flags default to
// false. A write stores the boolean the caller sent.
type Definition struct {
	Key         string
	Label       string
	Description string
	Scopes      []string
	Default     bool
}

// AppliesTo reports whether this flag may be stored for the given scope.
func (d Definition) AppliesTo(scope string) bool {
	return slices.Contains(d.Scopes, scope)
}

var catalog = []Definition{
	{
		Key:         FlagAPIRequestSignatureRequired,
		Label:       "Require a signature on API requests",
		Description: "Records whether this account requires a signature on API requests.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagDepositScanEnabled,
		Label:       "Deposit scan",
		Description: "Records whether deposit scanning is turned on for this account.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     true,
	},
	{
		Key:         FlagSweepEnabled,
		Label:       "Sweep",
		Description: "Records whether sweep is turned on for this account.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     true,
	},
	{
		Key:         FlagUser2FARequired,
		Label:       "Require two-factor authentication",
		Description: "Records whether members of this account must use two-factor authentication.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWalletCreationEnabled,
		Label:       "Wallet creation",
		Description: "Records whether this account can create wallets.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWebhookDeliveryEnabled,
		Label:       "Webhook delivery",
		Description: "Records whether webhook delivery is turned on for this account.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWithdrawalsEnabled,
		Label:       "Withdrawals",
		Description: "Records whether withdrawals are turned on for this account.",
		Scopes:      []string{ScopeGlobal, ScopeAccount},
		Default:     true,
	},
}

// All returns every account flag, sorted by key. The slice is a copy.
func All() []Definition {
	out := make([]Definition, len(catalog))
	copy(out, catalog)
	slices.SortFunc(out, func(a, b Definition) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	return out
}

// ForAccount returns the flags that apply to an account, in key order.
func ForAccount() []Definition {
	return forScope(ScopeAccount)
}

// ForGlobal returns the flags that apply to the platform, in key order.
// The default of each flag matches the account default.
func ForGlobal() []Definition {
	return forScope(ScopeGlobal)
}

func forScope(scope string) []Definition {
	out := make([]Definition, 0, len(catalog))
	for _, definition := range All() {
		if definition.AppliesTo(scope) {
			out = append(out, definition)
		}
	}
	return out
}

// Find returns the definition of a flag by its exact key.
func Find(key string) (Definition, bool) {
	for _, definition := range catalog {
		if definition.Key == key {
			return definition, true
		}
	}
	return Definition{}, false
}

// enabledValue is the stored boolean when the account has a row, and the
// catalog default when it does not. The caller does not insert a row.
func enabledValue(stored map[string]bool, definition Definition) bool {
	if stored == nil {
		return definition.Default
	}
	if value, ok := stored[definition.Key]; ok {
		return value
	}
	return definition.Default
}
