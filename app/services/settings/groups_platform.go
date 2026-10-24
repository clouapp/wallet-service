package settings

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// GroupDepositSignatures is the deposit signature sync's platform group, and
// KeySignatureChains its chain list, which the composition root shows with its
// environment value while no row stores it (PlatformFallback).
const (
	GroupDepositSignatures = "deposit_signatures"
	KeySignatureChains     = "chains"
)

const (
	groupDepositScan        = "deposit_scan"
	groupDepositSignatures  = GroupDepositSignatures
	groupWebhookDelivery    = "webhook_delivery"
	groupSweepLimits        = "sweep_limits"
	groupMailSMTP           = "mail_smtp"
	groupMailDelivery       = "mail_delivery"
	groupMailSES            = "mail_ses"
	groupMailMailgun        = "mail_mailgun"
	groupMailResend         = "mail_resend"
	groupMailPostmark       = "mail_postmark"
	groupPriceLookup        = "price_lookup"
	groupPriceCoinGecko     = "price_coingecko"
	groupPriceCoinMarketCap = "price_coinmarketcap"
	groupPriceCoinAPI       = "price_coinapi"
	groupProviderAlchemy    = "provider_alchemy"
	groupProviderHelius     = "provider_helius"
	groupProviderQuickNode  = "provider_quicknode"
	groupProviderEtherscan  = "provider_etherscan"

	sectionScanning = "scanning"
	sectionDelivery = "delivery"
	sectionSweep    = "sweep"

	keyBatchBlocks     = "batch_blocks"
	keyCatchUpBlocks   = "catch_up_blocks"
	keyScanConcurrency = "concurrency"
	keyMaxAttempts     = "max_attempts"
	keyTimeoutSeconds  = "timeout_seconds"

	keySignatureChains      = KeySignatureChains
	keySignatureInterval    = "interval_seconds"
	keySignatureConcurrency = "concurrency"
	keySignatureMaxPages    = "max_pages"

	// The deposit_signatures bounds match deposit.MinSignatureSyncInterval,
	// deposit.MaxSignatureSyncConcurrency and deposit.MaxSignatureSyncMaxPages. A stored
	// value outside them is invalid and the sync keeps the environment value.
	minSignatureIntervalSeconds = 15
	maxSignatureConcurrency     = 16
	maxSignatureMaxPages        = 1000

	// maxDepositScanConcurrency matches deposit.MaxScanConcurrency. A stored
	// value above it is invalid and the scanner keeps the environment window.
	maxDepositScanConcurrency = 32

	// S1.4.4 moves the webhook retry constants (MaxAttempts: 10, 10 s timeout)
	// into webhook_delivery. S1.4.8 validates attempts as 1..20.
	defaultWebhookMaxAttempts    = 10
	defaultWebhookTimeoutSeconds = 10
	minWebhookAttempts           = 1
	maxWebhookAttempts           = 20
	// maxWebhookTimeoutSeconds is the largest int that still fits in a
	// time.Duration of whole seconds. A larger stored value is not a timeout
	// delivery can wait, so it is rejected and a later read keeps the default.
	maxWebhookTimeoutSeconds = int(math.MaxInt64 / int64(time.Second))
)

func platformGroups() []Group {
	return []Group{
		{
			Name:    groupDepositScan,
			Scope:   ScopePlatform,
			Section: sectionScanning,
			Block:   "Deposits",
			Settings: []Definition{
				{
					Key:     keyBatchBlocks,
					Label:   "Batch blocks",
					Help:    "Blocks read in one cycle when the scanner is near the chain head. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
				{
					Key:     keyCatchUpBlocks,
					Label:   "Catch-up blocks",
					Help:    "Blocks read in one cycle while the scanner is behind the chain head. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
				{
					Key:     keyScanConcurrency,
					Label:   "Scan concurrency",
					Help:    "How many blocks are fetched at once. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
			},
			Validate: validateDepositScan,
		},
		depositSignaturesGroup(),
		{
			Name:    groupWebhookDelivery,
			Scope:   ScopePlatform,
			Section: sectionDelivery,
			Block:   "Retries",
			Settings: []Definition{
				{
					Key:     keyMaxAttempts,
					Label:   "Max attempts",
					Help:    "How many times a webhook is sent before delivery stops. From 1 to 20.",
					Type:    TypeInt,
					Default: func() any { return defaultWebhookMaxAttempts },
				},
				{
					Key:     keyTimeoutSeconds,
					Label:   "Timeout",
					Help:    "Seconds one delivery waits for the receiver.",
					Type:    TypeInt,
					Default: func() any { return defaultWebhookTimeoutSeconds },
				},
			},
			Validate: validateWebhookDelivery,
		},
		{
			Name:    groupSweepLimits,
			Scope:   ScopePlatform,
			Section: sectionSweep,
			Block:   "Sweep",
			// S1.4.4 moves the hard-coded LoadLimits defaults here. Counts are
			// positive integers. A blank daily cap is unlimited. S1.4.7 names
			// sweep.view and sweep.update on this group. Holding
			// settings.update is not that pair. No account role holds it.
			// There is no platform permission catalog, so a platform_admins
			// row stands in.
			ViewPermission:   policies.PermSweepView,
			UpdatePermission: policies.PermSweepUpdate,
			Settings:         sweepLimitDefinitions(),
			Validate:         validateSweepLimits,
		},
		mailSMTPGroup(),
		mailDeliveryGroup(),
		mailSESGroup(),
		mailMailgunGroup(),
		mailResendGroup(),
		mailPostmarkGroup(),
		priceLookupGroup(),
		priceCoinGeckoGroup(),
		priceCoinMarketCapGroup(),
		priceCoinAPIGroup(),
		providerAlchemyGroup(),
		providerHeliusGroup(),
		providerQuickNodeGroup(),
		providerEtherscanGroup(),
	}
}

func validateDepositScan(effective map[string]string) error {
	fields := map[string][]string{}
	batch, batchOK := nonNegativeSetting(effective[keyBatchBlocks])
	catchUp, catchUpOK := nonNegativeSetting(effective[keyCatchUpBlocks])
	concurrency, concurrencyOK := nonNegativeSetting(effective[keyScanConcurrency])
	if !batchOK {
		fields[keyBatchBlocks] = []string{"must be greater than or equal to 0"}
	}
	if !catchUpOK {
		fields[keyCatchUpBlocks] = []string{"must be greater than or equal to 0"}
	}
	if !concurrencyOK || concurrency > maxDepositScanConcurrency {
		fields[keyScanConcurrency] = []string{fmt.Sprintf("must be between 0 and %d", maxDepositScanConcurrency)}
	}
	if batchOK && catchUpOK && batch > 0 && catchUp > 0 && catchUp < batch {
		fields[keyCatchUpBlocks] = []string{"must not be smaller than batch_blocks"}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

func validateWebhookDelivery(effective map[string]string) error {
	fields := map[string][]string{}
	if _, ok := boundedSetting(effective[keyMaxAttempts], minWebhookAttempts, maxWebhookAttempts); !ok {
		fields[keyMaxAttempts] = []string{fmt.Sprintf("must be between %d and %d", minWebhookAttempts, maxWebhookAttempts)}
	}
	if message := timeoutSettingMessage(effective[keyTimeoutSeconds]); message != "" {
		fields[keyTimeoutSeconds] = []string{message}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

func timeoutSettingMessage(raw string) string {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return "must be greater than 0"
	}
	if parsed > maxWebhookTimeoutSeconds {
		return "is too large"
	}
	return ""
}

func boundedSetting(raw string, floor, ceiling int) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < floor || parsed > ceiling {
		return 0, false
	}
	return parsed, true
}

func nonNegativeSetting(raw string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

// depositSignaturesGroup is the deposit signature sync: the chains whose deposits
// are read account by account instead of block by block (their block scan pauses),
// how often, how many accounts at once, and how much history without a floor. Each
// field left unset keeps its DEPOSIT_SIGNATURE_* environment value.
func depositSignaturesGroup() Group {
	return Group{
		Name:    groupDepositSignatures,
		Scope:   ScopePlatform,
		Section: sectionScanning,
		Block:   "Signature sync",
		Settings: []Definition{
			{
				Key:   keySignatureChains,
				Label: "Chains",
				Help: "Chains whose deposits are read account by account (getSignaturesForAddress) instead of block by block; " +
					"their block scan pauses while they are listed, and resumes from its checkpoint when they are not. " +
					"Unset keeps DEPOSIT_SIGNATURE_CHAINS; an empty list turns the sync off.",
				Type:    TypeStringList,
				Options: []string{models.ChainSOL, models.ChainTSOL},
				Default: func() any { return []string{} },
			},
			{
				Key:     keySignatureInterval,
				Label:   "Interval",
				Help:    "Seconds between two syncs of a chain, at least 15. 0 keeps the environment default (600).",
				Type:    TypeInt,
				Default: func() any { return 0 },
			},
			{
				Key:     keySignatureConcurrency,
				Label:   "RPC concurrency",
				Help:    "How many accounts are read at once, from 1 to 16. 0 keeps the environment default (2).",
				Type:    TypeInt,
				Default: func() any { return 0 },
			},
			{
				Key:   keySignatureMaxPages,
				Label: "History pages",
				Help: "Pages of 1000 signatures read at most from an account's history when the chain has no scan checkpoint, " +
					"from 1 to 1000. 0 keeps the environment default (10).",
				Type:    TypeInt,
				Default: func() any { return 0 },
			},
		},
		Validate: validateDepositSignatures,
	}
}

func validateDepositSignatures(effective map[string]string) error {
	fields := map[string][]string{}
	if interval, ok := nonNegativeSetting(effective[keySignatureInterval]); !ok || (interval > 0 && interval < minSignatureIntervalSeconds) {
		fields[keySignatureInterval] = []string{fmt.Sprintf("must be 0 or at least %d", minSignatureIntervalSeconds)}
	}
	if concurrency, ok := nonNegativeSetting(effective[keySignatureConcurrency]); !ok || concurrency > maxSignatureConcurrency {
		fields[keySignatureConcurrency] = []string{fmt.Sprintf("must be between 0 and %d", maxSignatureConcurrency)}
	}
	if pages, ok := nonNegativeSetting(effective[keySignatureMaxPages]); !ok || pages > maxSignatureMaxPages {
		fields[keySignatureMaxPages] = []string{fmt.Sprintf("must be between 0 and %d", maxSignatureMaxPages)}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}
