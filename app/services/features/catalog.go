// Package features is the account feature-flag catalog and the service that
// reads and writes it. A flag is a named switch stored per account. Metadata
// lives here; the features table stores only the boolean an owner or admin
// wrote. A missing row is disabled. This slice has no global, user, or chain
// scope and does not cache the value: the row is the source of truth.
package features

import "slices"

// ScopeAccount is the only scope this slice stores. The path does not take a
// scope segment: every flag below is an account switch.
const ScopeAccount = "account"

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

// Definition is one named flag. Default is false: a missing row stays off,
// whatever a later platform default might be. This service does not read
// Default when a row is absent; it returns disabled.
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
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagDepositScanEnabled,
		Label:       "Deposit scan",
		Description: "Records whether deposit scanning is turned on for this account.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagSweepEnabled,
		Label:       "Sweep",
		Description: "Records whether sweep is turned on for this account.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagUser2FARequired,
		Label:       "Require two-factor authentication",
		Description: "Records whether members of this account must use two-factor authentication.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWalletCreationEnabled,
		Label:       "Wallet creation",
		Description: "Records whether this account can create wallets.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWebhookDeliveryEnabled,
		Label:       "Webhook delivery",
		Description: "Records whether webhook delivery is turned on for this account.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
	},
	{
		Key:         FlagWithdrawalsEnabled,
		Label:       "Withdrawals",
		Description: "Records whether withdrawals are turned on for this account.",
		Scopes:      []string{ScopeAccount},
		Default:     false,
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
	out := make([]Definition, 0, len(catalog))
	for _, definition := range All() {
		if definition.AppliesTo(ScopeAccount) {
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
