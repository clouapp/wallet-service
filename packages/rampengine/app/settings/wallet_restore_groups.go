package settings

import (
	"fmt"
	"strconv"
)

// GroupWalletRestore holds client wallet-restore step-up knobs.
const GroupWalletRestore = "wallet_restore"

const (
	KeyWalletRestoreVerifyThrottleMaxAttempts  = "verify_throttle_max_attempts"
	KeyWalletRestoreVerifyThrottleDecayMinutes = "verify_throttle_decay_minutes"

	DefaultWalletRestoreVerifyThrottleMaxAttempts  = 5
	DefaultWalletRestoreVerifyThrottleDecayMinutes = 1
)

func walletRestoreGroups() []Group {
	return []Group{{
		Name:    GroupWalletRestore,
		Section: SectionAuthentication,
		Settings: []Definition{
			{Key: KeyWalletRestoreVerifyThrottleMaxAttempts, Label: "Verify Throttle (max attempts)", Type: TypeInt,
				Help:    "Wrong OTP guesses allowed on POST /api/wallet-restore/email/verify per user before the bucket empties.",
				Default: func() any { return DefaultWalletRestoreVerifyThrottleMaxAttempts }},
			{Key: KeyWalletRestoreVerifyThrottleDecayMinutes, Label: "Verify Throttle (decay minutes)", Type: TypeInt,
				Help:    "How long until the verify throttle bucket fully refills.",
				Default: func() any { return DefaultWalletRestoreVerifyThrottleDecayMinutes }},
		},
		Validate: validateWalletRestoreGroup,
	}}
}

func validateWalletRestoreGroup(effective map[string]string) error {
	if err := intInRange(effective[KeyWalletRestoreVerifyThrottleMaxAttempts], KeyWalletRestoreVerifyThrottleMaxAttempts, 1, 60); err != nil {
		return err
	}
	return intInRange(effective[KeyWalletRestoreVerifyThrottleDecayMinutes], KeyWalletRestoreVerifyThrottleDecayMinutes, 1, 60)
}

func intInRange(raw, key string, min, max int) error {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
	}
	return nil
}
