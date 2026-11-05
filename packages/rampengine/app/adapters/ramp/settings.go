package ramp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/codes"
)

// SettingsSource is the narrow settings dependency this package reads.
//
// It is declared here rather than imported so the router never depends on the
// settings service, and so `app/settings` can import this package to declare
// the provider groups without creating a cycle.
//
// The name is not SettingsReader because app/services/admin/auth already owns
// that one, and mockery would write both mocks to the same file.
type SettingsSource interface {
	String(group, key string) (string, error)
	Bool(group, key string) (bool, error)
	Secret(group, key string) (string, error)
}

// ProviderGroup is the settings group holding one provider's admin state.
func ProviderGroup(code ProviderCode) string { return "provider_" + code }

// RailGroup is the settings group holding one rail's routing policy.
func RailGroup(rail Rail) string { return "rail_" + rail.String() }

// The keys inside those groups. They are constants because the registry, the
// migration and this reader all have to name them identically, and a typo in
// any one of the three is a silent misread rather than a compile error.
const (
	SettingEnabled          = "enabled"
	SettingMode             = "mode"
	SettingAPIKey           = "api_key"
	SettingWebhookPublicKey = "webhook_public_key"
	// SettingWebhookSecret is a shared HMAC secret, distinct from
	// SettingWebhookPublicKey's PEM-armoured asymmetric key. Bridge verifies a
	// delivery against a public key; Ozav verifies one with a shared secret
	// (`ozav_whsec_…`) over HMAC-SHA256. Reusing SettingWebhookPublicKey for
	// both would mislabel what the stored value actually is.
	SettingWebhookSecret = "webhook_secret"

	// SettingInstanceID is BlindPay's `in_…` instance. Every BlindPay path is
	// scoped by it, and it is what decides the environment: BlindPay has no
	// environment prefix on its API keys.
	SettingInstanceID = "instance_id"

	// The Solana mint and decimals of each stablecoin, as the provider moves
	// them. They are settings rather than constants because the sandbox
	// settles in a test token whose mint only the provider's instance knows.
	SettingSolanaUSDCMint     = "solana_usdc_mint"
	SettingSolanaUSDCDecimals = "solana_usdc_decimals"
	SettingSolanaUSDTMint     = "solana_usdt_mint"
	SettingSolanaUSDTDecimals = "solana_usdt_decimals"

	// SettingSolanaDelegateAddress is the address the provider's prepared
	// funding delegates the stablecoin to (an SPL ApproveChecked). A prepared
	// transaction naming any other delegate is refused.
	SettingSolanaDelegateAddress = "solana_delegate_address"

	// SettingSolanaRPCURL is the Solana node a signed delegation is sent
	// through before the payout is created. It is a secret because a hosted
	// node's URL carries its key.
	SettingSolanaRPCURL = "solana_rpc_url"

	// SettingProductionPayoutConfirmation liberates real-money bank payouts in
	// production. Only an admin holding providers.production_confirm may change it.
	SettingProductionPayoutConfirmation = "production_solana_payout_confirmation"

	SettingProviderOrder = "provider_order"
	SettingMinAmountBRL  = "min_amount_brl"
	SettingMaxAmountBRL  = "max_amount_brl"
	SettingMaxAmountUSD  = "max_amount_usd"

	SettingMinAmountBRLByToken = "min_amount_brl_by_token"
	// SettingMinAmountUSD is a USD rail's floor. On a Bridge USD payout rail it
	// is after XIP's fee: the crypto leaves the user's wallet without a request,
	// so the backend enforces it on the payout address call the app makes right
	// before the send (with the amount) and on the sandbox's simulated deposit.
	// On a BlindPay bank rail it is the floor of the quoted amount, and
	// SettingMaxAmountUSD is its ceiling.
	SettingMinAmountUSD = "min_amount_usd"

	SettingOzavOfframpFundingWindowMinutes = "ozav_offramp_funding_window_minutes"
	SettingOzavDepositMatchTolerance       = "ozav_deposit_match_tolerance"
	SettingOzavDeliveryMaxAttempts         = "ozav_delivery_max_attempts"
)

// ProviderState is one provider's admin-managed state.
type ProviderState struct {
	Code    ProviderCode
	Enabled bool
	Mode    string
}

// IsUsable reports whether the router may consider this provider at all.
func (p ProviderState) IsUsable() bool {
	return p.Enabled && (p.Mode == models.ProviderModeSandbox || p.Mode == models.ProviderModeProduction)
}

// RailPolicy is the admin-managed routing policy for one rail: which providers
// to try and in what order, XIP's cut, and the amount limits that back the
// amount_below_minimum / amount_above_maximum errors.
//
// Limits are in the currency the rail's fiat leg is bounded in: BRL on both Pix
// rails, where the PIX leg always settles in BRL, and USD on the bank rails,
// where the provider limits the stablecoin leaving the wallet in dollars (one
// pair is set, chosen by Rail.IsBank, and the other stays nil). A Bridge USD
// payout rail carries only MinUSD; the USD payin carries no limit at all. A nil
// limit is unbounded — distinct from a limit of zero.
type RailPolicy struct {
	Providers []ProviderCode
	// Fees is XIP's cut per provider, as stored ("1.50"). Only the providers
	// in FeeProviderCodesFor(rail) have an entry.
	Fees   map[ProviderCode]string
	MinBRL *string
	MaxBRL *string
	MinUSD *string
	MaxUSD *string
	// MinBRLByToken overrides MinBRL for specific ramp tokens (uppercase keys).
	MinBRLByToken map[string]string
	// OzavOfframpFundingWindowMinutes caps how long an off-ramp awaits managed-wallet funding.
	OzavOfframpFundingWindowMinutes int
	// OzavDepositMatchTolerance is the absolute token amount slack when matching deposits.
	OzavDepositMatchTolerance string
}

// EffectiveMinBRL returns the lower bound for a token: a per-token override
// when configured, otherwise the rail-wide minimum.
func (p RailPolicy) EffectiveMinBRL(token string) *string {
	token = strings.ToUpper(strings.TrimSpace(token))
	if override := strings.TrimSpace(p.MinBRLByToken[token]); override != "" {
		return &override
	}
	return p.MinBRL
}

// FeeFor returns the fee an order served by code is charged on this rail.
//
// A provider with no fee of its own is an error rather than "": an empty fee
// would reach the adapter as no fee at all. Wrapping ErrNoProviderAvailable is
// what makes a quote attempt on it fail over like any other unusable provider.
func (p RailPolicy) FeeFor(code ProviderCode) (string, error) {
	fee, ok := p.Fees[code]
	if !ok {
		return "", fmt.Errorf("rail fee for %q: %w", code, ErrNoProviderAvailable)
	}
	return fee, nil
}

// Config reads the provider stack's admin state out of settings. It is where
// the stored strings are parsed, so no consumer reimplements the CSV split or
// the empty-means-unbounded rule.
type Config struct{ settings SettingsSource }

func NewConfig(settings SettingsSource) *Config { return &Config{settings: settings} }

const (
	// DefaultOzavDeliveryMaxAttempts is the on-ramp delivery attempt ceiling
	// when the setting is absent or unreadable.
	DefaultOzavDeliveryMaxAttempts = 3
	// DefaultOzavOfframpFundingWindowMinutes is how long an Ozav off-ramp
	// awaits managed-wallet funding when the setting is absent or unreadable.
	DefaultOzavOfframpFundingWindowMinutes = 30
)

// OzavDeliveryMaxAttempts reads the pix_payin delivery attempt ceiling. It is
// the only reader of that setting, and it never fails: an unreadable value
// falls back to the default rather than stopping deliveries.
func (c *Config) OzavDeliveryMaxAttempts() int {
	if c == nil || c.settings == nil {
		return DefaultOzavDeliveryMaxAttempts
	}
	raw, err := c.settings.String(RailGroup(RailPixPayin), SettingOzavDeliveryMaxAttempts)
	if err != nil {
		return DefaultOzavDeliveryMaxAttempts
	}
	return positiveIntOr(raw, DefaultOzavDeliveryMaxAttempts)
}

// Provider returns one provider's state. A provider nobody has configured reads
// back as disabled in sandbox, which is what the registry defaults say.
func (c *Config) Provider(code ProviderCode) (ProviderState, error) {
	group := ProviderGroup(code)
	enabled, err := c.settings.Bool(group, SettingEnabled)
	if err != nil {
		return ProviderState{}, fmt.Errorf("ramp config: %s enabled: %w", code, err)
	}
	mode, err := c.settings.String(group, SettingMode)
	if err != nil {
		return ProviderState{}, fmt.Errorf("ramp config: %s mode: %w", code, err)
	}
	return ProviderState{Code: code, Enabled: enabled, Mode: mode}, nil
}

// Secret returns one of a provider's credentials in plaintext.
func (c *Config) Secret(code ProviderCode, key string) (string, error) {
	value, err := c.settings.Secret(ProviderGroup(code), key)
	if err != nil {
		return "", fmt.Errorf("ramp config: %s %s: %w", code, key, err)
	}
	return value, nil
}

// ProviderString returns one of a provider's plain settings — an id, a mint —
// as stored.
func (c *Config) ProviderString(code ProviderCode, key string) (string, error) {
	value, err := c.settings.String(ProviderGroup(code), key)
	if err != nil {
		return "", fmt.Errorf("ramp config: %s %s: %w", code, key, err)
	}
	return value, nil
}

// ProviderOrder returns one rail's provider_order alone, for a caller that
// asks only who the rail routes to.
func (c *Config) ProviderOrder(rail Rail) ([]ProviderCode, error) {
	order, err := c.settings.String(RailGroup(rail), SettingProviderOrder)
	if err != nil {
		return nil, fmt.Errorf("ramp config: %s %s: %w", rail, SettingProviderOrder, err)
	}
	return codes.ParseOrder(order), nil
}

func (c *Config) Rail(rail Rail) (RailPolicy, error) {
	group := RailGroup(rail)
	read := func(key string) (string, error) {
		value, err := c.settings.String(group, key)
		if err != nil {
			return "", fmt.Errorf("ramp config: %s %s: %w", rail, key, err)
		}
		return value, nil
	}

	order, err := read(SettingProviderOrder)
	if err != nil {
		return RailPolicy{}, err
	}
	feeCodes := FeeProviderCodesFor(rail)
	fees := make(map[ProviderCode]string, len(feeCodes))
	for _, code := range feeCodes {
		providerFee, err := read(FeeSettingKey(code))
		if err != nil {
			return RailPolicy{}, err
		}
		fees[code] = providerFee
	}
	policy := RailPolicy{Providers: codes.ParseOrder(order), Fees: fees}

	// A bank rail limits in USD and declares no BRL pair, so it returns before
	// any BRL key, per-token minimum or Ozav key is asked of its group.
	if rail.IsBank() {
		minKey, maxKey := LimitSettingKeys(rail)
		minLimit, err := read(minKey)
		if err != nil {
			return RailPolicy{}, err
		}
		maxLimit, err := read(maxKey)
		if err != nil {
			return RailPolicy{}, err
		}
		policy.MinUSD, policy.MaxUSD = optionalLimit(minLimit), optionalLimit(maxLimit)
		return policy, nil
	}

	// Each rail declares only the limits of its own currency, so asking a USD
	// group for min_amount_brl would be a read of a key that does not exist.
	if rail.FiatCurrency() == models.FiatCurrencyBRL {
		minBRL, err := read(SettingMinAmountBRL)
		if err != nil {
			return RailPolicy{}, err
		}
		maxBRL, err := read(SettingMaxAmountBRL)
		if err != nil {
			return RailPolicy{}, err
		}
		byTokenRaw, err := read(SettingMinAmountBRLByToken)
		if err != nil {
			return RailPolicy{}, err
		}
		byToken, err := ParseMinAmountBRLByTokenSetting(byTokenRaw)
		if err != nil {
			return RailPolicy{}, fmt.Errorf("ramp config: %s %s: %w", rail, SettingMinAmountBRLByToken, err)
		}
		policy.MinBRL = optionalLimit(minBRL)
		policy.MaxBRL = optionalLimit(maxBRL)
		policy.MinBRLByToken = byToken
	}
	if rail == RailPixPayout {
		window, err := read(SettingOzavOfframpFundingWindowMinutes)
		if err != nil {
			return RailPolicy{}, err
		}
		tolerance, err := read(SettingOzavDepositMatchTolerance)
		if err != nil {
			return RailPolicy{}, err
		}
		if strings.TrimSpace(tolerance) == "" {
			tolerance = "0"
		}
		policy.OzavOfframpFundingWindowMinutes = positiveIntOr(window, DefaultOzavOfframpFundingWindowMinutes)
		policy.OzavDepositMatchTolerance = tolerance
	}
	if rail.IsUSDPayout() {
		minUSD, err := read(SettingMinAmountUSD)
		if err != nil {
			return RailPolicy{}, err
		}
		policy.MinUSD = optionalLimit(minUSD)
	}
	return policy, nil
}

// LimitSettingKeys names a rail group's two limit keys. A bank rail limits in
// USD and declares no BRL pair, so reading the BRL keys on one would fail; the
// registry and this reader both pick the pair through here.
func LimitSettingKeys(rail Rail) (minKey, maxKey string) {
	if rail.IsBank() {
		return SettingMinAmountUSD, SettingMaxAmountUSD
	}
	return SettingMinAmountBRL, SettingMaxAmountBRL
}

// positiveIntOr reads a stored count. The admin write validates these, so a
// value that does not parse (or is below one) is a hand-edited row, and the
// default keeps the rail open instead of failing every estimate on it.
func positiveIntOr(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// ParseMinAmountBRLByTokenSetting reads the optional JSON map stored on a rail
// group. Blank means no overrides.
func ParseMinAmountBRLByTokenSetting(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := make(map[string]string, len(parsed))
	for token, amount := range parsed {
		key := strings.ToUpper(strings.TrimSpace(token))
		if key == "" {
			return nil, errors.New("empty token key")
		}
		trimmed := strings.TrimSpace(amount)
		value, ok := new(big.Rat).SetString(trimmed)
		if !ok || trimmed == "" {
			return nil, fmt.Errorf("invalid decimal for token %s", key)
		}
		if value.Sign() < 0 {
			return nil, fmt.Errorf("negative amount for token %s", key)
		}
		out[key] = trimmed
	}
	return out, nil
}

// FormatProviderOrder renders codes back into the stored CSV.
func FormatProviderOrder(codes []ProviderCode) string { return strings.Join(codes, ",") }

// optionalLimit turns a blank stored limit into "no limit". Returning a pointer
// to "" would compare as a bound of zero and reject every order.
func optionalLimit(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

// AllProviderCodes is the closed set of providers this stack knows, in the
// order a fan-out visits them.
//
// It lives here rather than in app/settings because the fan-out reads it and
// must not import the settings package. The same list is declared for the
// admin registry in app/settings/ramp_groups.go; both are derived from the
// constants in app/models, which is the actual source of truth.
func AllProviderCodes() []ProviderCode {
	return []ProviderCode{ProviderBridge, ProviderMutualPay, ProviderOzav, ProviderBlindPay}
}

// FeeProviderCodes is the closed set of providers the pix rails charge XIP's
// fee for, in the order the admin form lists them. MutualPay is absent on
// purpose: it is not used, and a fee field for it would be a number nobody is
// charged. BlindPay is present because the pix rails offer it too, with a fee
// of its own.
func FeeProviderCodes() []ProviderCode {
	return []ProviderCode{ProviderBridge, ProviderOzav, ProviderBlindPay}
}

// FeeProviderCodesFor is the closed set of providers one rail charges XIP's
// fee for. The pix rails keep FeeProviderCodes; the bank rails are BlindPay's
// alone, since no other provider serves them; the Bridge USD rails charge for
// the two providers that ever served them, never for BlindPay.
func FeeProviderCodesFor(rail Rail) []ProviderCode {
	switch {
	case rail.IsBank():
		return []ProviderCode{ProviderBlindPay}
	case rail.FiatCurrency() == models.FiatCurrencyUSD:
		return []ProviderCode{ProviderBridge, ProviderOzav}
	}
	return FeeProviderCodes()
}

// FeeSettingKey is the rail-group key holding one provider's fee. The
// registry and this reader both name it through here; migrations freeze the
// literal instead.
func FeeSettingKey(code ProviderCode) string { return "fee_percent_" + code }

// EligibleProviders returns, in AllProviderCodes order, every provider an
// operator has switched on whose mode agrees with appEnv.
//
// It reports usable providers, not implemented ones: whether an adapter exists
// behind a code is the factory's question, and a caller that needs both asks
// both. Keeping them apart is what lets an operator enable a provider before
// its adapter ships without the two facts contradicting each other.
//
// ValidateMode is the identical check Router.evaluate applies at the point money
// would actually move. Every caller that must not treat a mode-mismatched
// provider as available shares it through here instead of keeping a second
// copy that could drift.
//
// onModeMismatch, when non-nil, is called for each usable-but-mismatched
// provider before it is dropped — never for one that was simply not enabled,
// which is not a misconfiguration worth a caller's attention. The KYC fan-out
// passes a hook that logs a warning, since a mismatch there meant a real side
// effect was about to happen; the capabilities read model, which only reads
// and is computed on every page load, passes nil and stays quiet.
func (c *Config) EligibleProviders(appEnv string, onModeMismatch func(code ProviderCode, err error)) ([]ProviderCode, error) {
	codes := AllProviderCodes()
	out := make([]ProviderCode, 0, len(codes))
	for _, code := range codes {
		state, err := c.Provider(code)
		if err != nil {
			return nil, err
		}
		if !state.IsUsable() {
			continue
		}
		if err := ValidateMode(state.Mode, appEnv); err != nil {
			if onModeMismatch != nil {
				onModeMismatch(code, err)
			}
			continue
		}
		out = append(out, code)
	}
	return out, nil
}
