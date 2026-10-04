package settings

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	groupAccountSecurity    = "account_security"
	groupAccountWebhooks    = "account_webhooks"
	groupAccountSweepLimits = "account_sweep_limits"

	sectionSecurity = "security"
	sectionWebhooks = "webhooks"
	sectionLimits   = "limits"

	keyRequire2FA                   = "require_2fa"
	keySessionIdleMinutes           = "session_idle_minutes"
	keySigningAlgorithm             = "signing_algorithm"
	keySigningSecret                = "signing_secret"
	keyDefaultEvents                = "default_events"
	keyMaxAddressesEVM              = "max_addresses_evm"
	keyMaxAddressesSolana           = "max_addresses_solana"
	keyMaxAddressesBitcoin          = "max_addresses_bitcoin"
	keyMaxConsolidateRequestsPerDay = "max_consolidate_requests_per_day"
	keyDailyWithdrawCapUSD          = "daily_withdraw_cap_usd"

	signingAlgorithmHMACSHA256 = "hmac-sha256"

	// A secret group names its own pair. The account route still gates on
	// settings.view and settings.update; this pair is what a later check
	// requires on top of that, so the route permission is not the credential.
	permAccountWebhooksView   = "settings.webhooks.view"
	permAccountWebhooksUpdate = "settings.webhooks.update"

	defaultSessionIdleMinutes           = 30
	minSessionIdleMinutes               = 5
	maxSessionIdleMinutes               = 10080
	defaultMaxAddressesEVM              = 100
	defaultMaxAddressesSolana           = 25
	defaultMaxAddressesBitcoin          = 100
	defaultMaxConsolidateRequestsPerDay = 50
)

func accountGroups() []Group {
	return []Group{
		{
			Name:      groupAccountSecurity,
			Scope:     ScopeAccount,
			ManagedBy: ManagedByAccount,
			Section:   sectionSecurity,
			Block:     "Session",
			Settings: []Definition{
				{
					Key:     keyRequire2FA,
					Label:   "Require two-factor authentication",
					Help:    "Members must confirm a second factor before they use this account.",
					Type:    TypeBool,
					Default: func() any { return false },
				},
				{
					Key:     keySessionIdleMinutes,
					Label:   "Session idle timeout",
					Help:    "Minutes of inactivity before a dashboard session is signed out.",
					Type:    TypeInt,
					Default: func() any { return defaultSessionIdleMinutes },
				},
			},
			Validate: validateSessionIdle,
		},
		{
			Name:             groupAccountWebhooks,
			Scope:            ScopeAccount,
			ManagedBy:        ManagedByAccount,
			Section:          sectionWebhooks,
			Block:            "Delivery",
			ViewPermission:   permAccountWebhooksView,
			UpdatePermission: permAccountWebhooksUpdate,
			Settings: []Definition{
				{
					Key:     keySigningAlgorithm,
					Label:   "Signing algorithm",
					Help:    "How webhook payloads are signed.",
					Type:    TypeString,
					Options: []string{signingAlgorithmHMACSHA256},
					Default: func() any { return signingAlgorithmHMACSHA256 },
				},
				{
					Key:     keySigningSecret,
					Label:   "Signing secret",
					Help:    "Default secret for webhook signatures. Leave blank to keep the saved value.",
					Type:    TypeString,
					Secret:  true,
					Default: func() any { return "" },
				},
				{
					Key:     keyDefaultEvents,
					Label:   "Default events",
					Help:    "Events selected when a new webhook is created.",
					Type:    TypeStringList,
					Options: webhookEventOptions(),
					Default: func() any { return []string{string(types.EventDepositConfirmed)} },
				},
			},
		},
		{
			Name:      groupAccountSweepLimits,
			Scope:     ScopeAccount,
			ManagedBy: ManagedByPlatform,
			Inherits:  "sweep_limits",
			Section:   sectionLimits,
			Block:     "Sweep",
			Settings: []Definition{
				{
					Key:     keyMaxAddressesEVM,
					Label:   "Max addresses per EVM sweep",
					Type:    TypeInt,
					Default: func() any { return defaultMaxAddressesEVM },
				},
				{
					Key:     keyMaxAddressesSolana,
					Label:   "Max addresses per Solana sweep",
					Type:    TypeInt,
					Default: func() any { return defaultMaxAddressesSolana },
				},
				{
					Key:     keyMaxAddressesBitcoin,
					Label:   "Max addresses per Bitcoin sweep",
					Type:    TypeInt,
					Default: func() any { return defaultMaxAddressesBitcoin },
				},
				{
					Key:     keyMaxConsolidateRequestsPerDay,
					Label:   "Max consolidate requests per day",
					Type:    TypeInt,
					Default: func() any { return defaultMaxConsolidateRequestsPerDay },
				},
				{
					Key:     keyDailyWithdrawCapUSD,
					Label:   "Daily withdrawal cap (USD)",
					Help:    "Blank means no cap. Sent and stored as a decimal string.",
					Type:    TypeDecimal,
					Default: func() any { return "" },
				},
			},
			Validate: validateSweepLimits,
		},
	}
}

func webhookEventOptions() []string {
	return []string{
		string(types.EventDepositConfirming),
		string(types.EventDepositConfirmed),
		string(types.EventDepositFailed),
		string(types.EventDepositPending),
		string(types.EventSweepBroadcast),
		string(types.EventSweepConfirmed),
		string(types.EventWalletGasStatusChanged),
		string(types.EventWithdrawalBroadcast),
		string(types.EventWithdrawalBroadcasting),
		string(types.EventWithdrawalConfirmed),
		string(types.EventWithdrawalFailed),
		string(types.EventWithdrawalSweepRequired),
	}
}

func validateSessionIdle(effective map[string]string) error {
	minutes, err := strconv.Atoi(effective[keySessionIdleMinutes])
	if err != nil || minutes < minSessionIdleMinutes || minutes > maxSessionIdleMinutes {
		return &ValidationError{Fields: map[string][]string{
			keySessionIdleMinutes: {fmt.Sprintf("must be between %d and %d", minSessionIdleMinutes, maxSessionIdleMinutes)},
		}}
	}
	return nil
}

func validateSweepLimits(effective map[string]string) error {
	fields := map[string][]string{}
	for _, key := range []string{
		keyMaxAddressesEVM,
		keyMaxAddressesSolana,
		keyMaxAddressesBitcoin,
		keyMaxConsolidateRequestsPerDay,
	} {
		n, err := strconv.Atoi(effective[key])
		if err != nil || n <= 0 {
			fields[key] = []string{"must be greater than 0"}
		}
	}
	capUSD := strings.TrimSpace(effective[keyDailyWithdrawCapUSD])
	if capUSD != "" {
		parsed, ok := new(big.Rat).SetString(capUSD)
		if !ok || parsed.Sign() < 0 {
			fields[keyDailyWithdrawCapUSD] = []string{"must be a decimal string greater than or equal to 0"}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}
