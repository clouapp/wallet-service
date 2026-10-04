package settings

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	groupDepositScan        = "deposit_scan"
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
			Name:     groupSweepLimits,
			Scope:    ScopePlatform,
			Section:  sectionSweep,
			Block:    "Sweep",
			Settings: sweepLimitDefinitions(),
			// S1.4.4 moves the hard-coded LoadLimits defaults here. Counts are
			// positive integers. A blank daily cap is unlimited. This group
			// names no permission: a platform_admins row is the gate.
			Validate: validateSweepLimits,
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
