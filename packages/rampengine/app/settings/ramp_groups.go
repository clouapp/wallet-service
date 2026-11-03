package settings

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The permissions the provider and rail groups declare. They are their own
// grants, not settings.*, because writing a live API key and changing where
// money goes is a different blast radius from editing the SMTP host — and the
// standard operator role holds settings.update but not providers.update.
const (
	permProvidersView   = "providers.view"
	permProvidersUpdate = "providers.update"
)

// SectionProviders is the single settings page the whole provider stack shows
// on. Each provider and each rail still saves on its own — the section only
// decides what the nav lists.
const SectionProviders = "providers"

// The two blocks that page reads as.
const (
	BlockProviders = "providers"
	BlockRails     = "rails"
)

// rampGroups declares one group per provider and one per rail, all on the one
// providers page.
//
// base_url and auth_scheme are deliberately absent: they are constants of each
// adapter, and no stored value should be able to make an adapter send a header
// it does not implement.
func rampGroups() []Group {
	rails := slices.Concat(ramp.AllRails(), ramp.BankRails())
	groups := make([]Group, 0, len(ramp.AllProviderCodes())+len(rails))
	for _, code := range ramp.AllProviderCodes() {
		groups = append(groups, providerGroup(code))
	}
	for _, rail := range rails {
		groups = append(groups, railGroup(rail))
	}
	return groups
}

func providerGroup(code string) Group {
	defs := []Definition{
		{Key: ramp.SettingEnabled, Label: "Enabled", Type: TypeBool,
			Default: func() any { return false }},
		{Key: ramp.SettingMode, Label: "Mode", Type: TypeString,
			Help:    "sandbox or production. An enabled provider's mode must agree with APP_ENV.",
			Options: []string{models.ProviderModeSandbox, models.ProviderModeProduction},
			Default: func() any { return models.ProviderModeSandbox }},
	}
	// Only a provider with a real adapter behind it gets credentials to spend.
	// MutualPay stays a stub and gets keys when it gets an implementation, not
	// before.
	switch code {
	case models.ProviderBridge:
		defs = append(defs,
			Definition{Key: ramp.SettingAPIKey, Label: "API Key", Type: TypeString, Secret: true,
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingWebhookPublicKey, Label: "Webhook Public Key",
				Type: TypeRSAPublicKeyPEM, Secret: true,
				Help:    "PEM the adapter verifies every inbound delivery against. Paste it whole, line breaks and BEGIN/END lines included.",
				Default: func() any { return "" }},
		)
	case models.ProviderOzav:
		defs = append(defs,
			Definition{Key: ramp.SettingAPIKey, Label: "API Key", Type: TypeString, Secret: true,
				Help:    "Prefixed ozav_sk_test_ or ozav_sk_live_ — the prefix must agree with Mode.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingWebhookSecret, Label: "Webhook Secret", Type: TypeString, Secret: true,
				Help:    "Shared HMAC secret Ozav signs deliveries with. Paste it whole, including the ozav_whsec_ prefix.",
				Default: func() any { return "" }},
		)
	case models.ProviderBlindPay:
		defs = append(defs,
			Definition{Key: ramp.SettingAPIKey, Label: "API Key", Type: TypeString, Secret: true,
				Help:    "BlindPay API key. It carries no environment marker: the instance below decides sandbox or production.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingInstanceID, Label: "Instance ID", Type: TypeString,
				Help:    "The in_… instance every request is scoped to. A development instance is sandbox, a production instance is live.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingWebhookSecret, Label: "Webhook Secret", Type: TypeString, Secret: true,
				Help:    "Svix signing secret from the BlindPay dashboard. Paste it whole, including the whsec_ prefix.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingSolanaUSDCMint, Label: "Solana USDC Mint", Type: TypeString,
				Help:    "Mint of the token USDC settles in. On a development instance this is BlindPay's USDB test token.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingSolanaUSDCDecimals, Label: "Solana USDC Decimals", Type: TypeInt,
				Default: func() any { return 6 }},
			Definition{Key: ramp.SettingSolanaUSDTMint, Label: "Solana USDT Mint", Type: TypeString,
				Help:    "Mint of the token USDT settles in. On a development instance this is BlindPay's USDB test token.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingSolanaUSDTDecimals, Label: "Solana USDT Decimals", Type: TypeInt,
				Default: func() any { return 6 }},
			Definition{Key: ramp.SettingSolanaDelegateAddress, Label: "Solana Delegate Address", Type: TypeString,
				Help:    "Address BlindPay's prepared funding delegates the stablecoin to. A prepared transaction naming any other delegate is refused.",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingSolanaRPCURL, Label: "Solana RPC URL", Type: TypeString, Secret: true,
				Help:    "Node the signed delegation is sent through before a payout is created. Use a dedicated endpoint in production; the public one rate limits. It must match the network of the mints above (devnet for a sandbox instance).",
				Default: func() any { return "" }},
			Definition{Key: ramp.SettingProductionPayoutConfirmation, Label: "Confirm production Solana payouts", Type: TypeBool,
				UpdatePermission: "providers.production_confirm",
				Help:             "Liberates real-money bank payouts in production. Only an admin with providers.production_confirm can change it.",
				Default:          func() any { return false }},
		)
	}
	return Group{
		Name:             ramp.ProviderGroup(code),
		Settings:         defs,
		Section:          SectionProviders,
		Block:            BlockProviders,
		ViewPermission:   permProvidersView,
		UpdatePermission: permProvidersUpdate,
		Validate:         providerValidator(code),
	}
}

// feeLabels names each fee-bearing provider the way the admin form reads it.
var feeLabels = map[ramp.ProviderCode]string{
	models.ProviderBridge:   "Bridge",
	models.ProviderOzav:     "Ozav",
	models.ProviderBlindPay: "BlindPay",
}

// railFeeDefinitions declares one fee per provider rail charges for, in the
// order FeeProviderCodesFor lists them.
func railFeeDefinitions(rail ramp.Rail) []Definition {
	feeCodes := ramp.FeeProviderCodesFor(rail)
	defs := make([]Definition, 0, len(feeCodes))
	for _, code := range feeCodes {
		defs = append(defs, Definition{
			Key:     ramp.FeeSettingKey(code),
			Label:   "Fee Percent (" + feeLabels[code] + ")",
			Type:    TypeDecimal,
			Help:    "XIP's cut on orders " + feeLabels[code] + " serves, as a percentage.",
			Default: func() any { return "0.00" },
		})
	}
	return defs
}

// railLimitDefinitions declares the rail's limit pair in the currency it is
// bounded in: BRL on the pix rails, USD on the bank rails.
func railLimitDefinitions(rail ramp.Rail) []Definition {
	minKey, maxKey := ramp.LimitSettingKeys(rail)
	currency := "BRL"
	if rail.IsBank() {
		currency = "USD"
	}
	return []Definition{
		{Key: minKey, Label: "Minimum Amount (" + currency + ")", Type: TypeDecimal,
			Help:    "Empty means no lower bound.",
			Default: func() any { return "" }},
		{Key: maxKey, Label: "Maximum Amount (" + currency + ")", Type: TypeDecimal,
			Help:    "Empty means no upper bound.",
			Default: func() any { return "" }},
	}
}

// defaultProviderOrder is what a rail routes through before an operator says
// otherwise. A bank or USD rail starts closed: an open default would route
// real payouts through a provider nobody chose for them.
func defaultProviderOrder(rail ramp.Rail) func() any {
	if rail.IsBank() || rail.FiatCurrency() == models.FiatCurrencyUSD {
		return func() any { return "" }
	}
	return func() any { return models.ProviderBridge }
}

// railProviderOptions is the vocabulary a rail's order may name: the
// providers that can serve it. A bank rail offers BlindPay alone, so an
// operator cannot route a bank payout through a provider that only speaks Pix.
func railProviderOptions(rail ramp.Rail) []string {
	if rail.IsBank() {
		return []string{models.ProviderBlindPay}
	}
	return []string{models.ProviderBridge, models.ProviderMutualPay, models.ProviderOzav, models.ProviderBlindPay}
}

func railGroup(rail ramp.Rail) Group {
	return Group{
		Name:             ramp.RailGroup(rail),
		Settings:         railDefinitions(rail),
		Section:          SectionProviders,
		Block:            BlockRails,
		ViewPermission:   permProvidersView,
		UpdatePermission: permProvidersUpdate,
		Validate:         railValidator(rail),
	}
}

// railDefinitions is what one rail's card holds. Every rail has an order and a
// fee per provider; the limits follow the rail's currency. A USD or bank rail
// is born closed (an empty order): opening it is an operator's act, not a
// deploy's.
func railDefinitions(rail ramp.Rail) []Definition {
	defs := []Definition{
		{Key: ramp.SettingProviderOrder, Label: "Provider Order", Type: TypeStringList,
			Help: "Providers are tried in this order. An empty list closes the rail.",
			// The closed vocabulary a rail order may name. Declaring it as
			// the setting's Options is what keeps a typo out — it would
			// otherwise silently remove a provider from the rail — and
			// what lets the admin form offer the codes instead of asking
			// an operator to spell them.
			Options: railProviderOptions(rail),
			Default: defaultProviderOrder(rail)},
	}
	fees := railFeeDefinitions(rail)
	if rail.IsUSDPayout() {
		// The fee is written into each liquidation address when it is
		// created; changing it here does not reach the addresses that exist.
		for i := range fees {
			fees[i].Help += " Applies to payout addresses created after the change; existing addresses keep the fee they were created with."
		}
	}
	defs = append(defs, fees...)
	if rail.IsBank() {
		defs = append(defs, railLimitDefinitions(rail)...)
	}
	if rail.FiatCurrency() == models.FiatCurrencyBRL {
		defs = append(defs, railLimitDefinitions(rail)...)
		defs = append(defs,
			Definition{Key: ramp.SettingMinAmountBRLByToken, Label: "Minimum Amount by Token (BRL)", Type: TypeString,
				Help:    `Optional JSON object, e.g. {"USDC":"400.00","BRS":"10.00"}. Overrides the rail minimum for that token.`,
				Default: func() any { return "" }},
		)
	}
	if rail == ramp.RailPixPayout {
		defs = append(defs,
			Definition{Key: ramp.SettingOzavOfframpFundingWindowMinutes, Label: "Ozav off-ramp funding window (minutes)", Type: TypeInt,
				Help:    "How long an Ozav off-ramp awaiting managed-wallet funding may stay open. Between 1 and 1440.",
				Default: func() any { return ramp.DefaultOzavOfframpFundingWindowMinutes }},
			Definition{Key: ramp.SettingOzavDepositMatchTolerance, Label: "Ozav deposit match tolerance", Type: TypeDecimal,
				Help: "Absolute token slack when matching deposit.credited to an off-ramp order.", Default: func() any { return "0" }},
		)
	}
	if rail == ramp.RailPixPayin {
		defs = append(defs,
			Definition{Key: ramp.SettingOzavDeliveryMaxAttempts, Label: "Ozav delivery max attempts", Type: TypeInt,
				Help:    "Delivery attempts for an on-ramp before retries stop and the order waits, still processing, for an operator. Between 1 and 10.",
				Default: func() any { return ramp.DefaultOzavDeliveryMaxAttempts }},
		)
	}
	if rail.IsUSDPayout() {
		defs = append(defs, Definition{Key: ramp.SettingMinAmountUSD, Label: "Minimum Amount (USD)", Type: TypeDecimal,
			Help: "Floor the backend enforces before the app sends: amount × (1 − fee) must reach it. " +
				"Bridge keeps a deposit below its own minimum.",
			Default: func() any { return "2.00" }})
	}
	return defs
}

// validateProviderGroup refuses a combination that would move real money in the
// wrong place — at the write, not at the next boot. The router re-checks the
// mode when it resolves, which is the point where money actually moves; this is
// the earlier, louder half of the same rule.
func validateProviderGroup(effective map[string]string) error {
	// The mode vocabulary is the Options on the definition, so by the time a
	// value reaches here it is already one of the two. What is left is the part
	// no single field can answer: whether this mode may be enabled at all.
	//
	// A disabled provider may sit in either mode — nothing routes through it,
	// and forcing the operator to fix the mode before they may disable it would
	// be backwards.
	if effective[ramp.SettingEnabled] != "true" {
		return nil
	}
	return ramp.ValidateMode(effective[ramp.SettingMode], facades.Config().GetString("app.env"))
}

// providerValidator picks a provider group's validator: the shared mode rule,
// plus the shape checks of the settings only that provider declares.
func providerValidator(code string) func(map[string]string) error {
	if code == models.ProviderBlindPay {
		return validateBlindPayGroup
	}
	return validateProviderGroup
}

// base58Mint is a Solana public key's text form: 32 bytes in base58, which is
// 32 to 44 characters from an alphabet without 0, O, I and l.
var base58Mint = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)

// blindPayInstanceID is the in_… identifier BlindPay scopes every request to.
// A bare prefix check would let "in_ x" or "in_/.." through, and each call
// would then fail as "instance not found".
var blindPayInstanceID = regexp.MustCompile(`^in_[A-Za-z0-9]+$`)

// validateBlindPayGroup refuses a BlindPay setting whose shape cannot be
// right, at the write, so a typo is caught by the operator who made it rather
// than by the first user whose payout fails. Blank values pass: a provider
// being configured is saved field by field.
//
// The secrets are not here: the effective state a group validator receives
// excludes them (they are ciphertext by then). A webhook secret without its
// whsec_ prefix is refused where it is used, by the webhook verifier.
func validateBlindPayGroup(effective map[string]string) error {
	if err := validateProviderGroup(effective); err != nil {
		return err
	}
	if id := effective[ramp.SettingInstanceID]; id != "" && !blindPayInstanceID.MatchString(id) {
		return fmt.Errorf("%s must look like in_…", ramp.SettingInstanceID)
	}
	for _, key := range []string{ramp.SettingSolanaUSDCMint, ramp.SettingSolanaUSDTMint, ramp.SettingSolanaDelegateAddress} {
		if address := effective[key]; address != "" && !base58Mint.MatchString(address) {
			return fmt.Errorf("%s is not a Solana address", key)
		}
	}
	for _, key := range []string{ramp.SettingSolanaUSDCDecimals, ramp.SettingSolanaUSDTDecimals} {
		decimals, err := strconv.Atoi(effective[key])
		if err != nil || decimals < 0 || decimals > 18 {
			return fmt.Errorf("%s must be an integer from 0 to 18", key)
		}
	}
	return nil
}

// maxBlindPayFeeBps is BlindPay's ceiling on a partner fee: 99%.
const maxBlindPayFeeBps = 9900

// validateOzavRailSettings checks the Ozav keys a rail carries; each rail
// declares only some of them, and an absent key is not checked.
func validateOzavRailSettings(effective map[string]string) error {
	if err := intSettingInRange(effective, ramp.SettingOzavOfframpFundingWindowMinutes, 1, 1440); err != nil {
		return err
	}
	if err := intSettingInRange(effective, ramp.SettingOzavDeliveryMaxAttempts, 1, 10); err != nil {
		return err
	}
	tolerance, ok := effective[ramp.SettingOzavDepositMatchTolerance]
	if !ok || strings.TrimSpace(tolerance) == "" {
		return nil
	}
	if _, err := ramp.ScaleAmount(tolerance, 6); err != nil {
		return fmt.Errorf("%s must be a non-negative decimal", ramp.SettingOzavDepositMatchTolerance)
	}
	return nil
}

func intSettingInRange(effective map[string]string, key string, low, high int) error {
	raw, ok := effective[key]
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < low || n > high {
		return fmt.Errorf("%s must be an integer between %d and %d", key, low, high)
	}
	return nil
}

// railValidator checks one rail group's effective state: every fee the rail
// declares, and its limit pair in whichever currency it is bounded in.
func railValidator(rail ramp.Rail) func(map[string]string) error {
	minKey, maxKey := ramp.LimitSettingKeys(rail)
	return func(effective map[string]string) error {
		// Validated through the same conversion the adapters use, so a fee
		// the provider would reject cannot be stored.
		for _, code := range ramp.FeeProviderCodesFor(rail) {
			key := ramp.FeeSettingKey(code)
			bps, err := ramp.FeePercentToSpreadBps(effective[key])
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			if code == ramp.ProviderBlindPay {
				if rail.IsBank() && bps != 0 {
					return fmt.Errorf("%s must stay 0 on a bank rail", key)
				}
				if bps > maxBlindPayFeeBps {
					return fmt.Errorf("%s must not exceed 99", key)
				}
			}
		}
		minLimit, err := parseLimit(minKey, effective[minKey])
		if err != nil {
			return err
		}
		maxLimit, err := parseLimit(maxKey, effective[maxKey])
		if err != nil {
			return err
		}
		if minLimit != nil && maxLimit != nil && minLimit.Cmp(maxLimit) > 0 {
			return fmt.Errorf("%s must not exceed %s", minKey, maxKey)
		}
		if rail.IsBank() {
			return nil
		}
		minUSD, err := parseLimit(ramp.SettingMinAmountUSD, effective[ramp.SettingMinAmountUSD])
		if err != nil {
			return err
		}
		if minUSD != nil && minUSD.Sign() < 0 {
			return fmt.Errorf("%s must not be negative", ramp.SettingMinAmountUSD)
		}
		overrides, err := ramp.ParseMinAmountBRLByTokenSetting(effective[ramp.SettingMinAmountBRLByToken])
		if err != nil {
			return err
		}
		for token, amount := range overrides {
			if _, err := parseLimit(ramp.SettingMinAmountBRLByToken+"."+token, amount); err != nil {
				return err
			}
		}
		return validateOzavRailSettings(effective)
	}
}

// validateRailGroup is the validator both pix rails share: they declare the
// same fees and the same BRL limits.
var validateRailGroup = railValidator(ramp.RailPixPayout)

// parseLimit reads one bound. Blank is unbounded, which is not an error.
func parseLimit(key, raw string) (*big.Rat, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, fmt.Errorf("%s %q is not a number", key, raw)
	}
	return value, nil
}
