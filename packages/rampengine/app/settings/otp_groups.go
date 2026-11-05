package settings

import (
	"fmt"
	"strconv"
)

// GroupOTP holds the one-time-code knobs read on every send.
const GroupOTP = "otp"

const (
	KeyOTPCodeTTLMinutes        = "code_ttl_minutes"
	KeyOTPResendCooldownSeconds = "resend_cooldown_seconds"

	DefaultOTPCodeTTLMinutes        = 15
	DefaultOTPResendCooldownSeconds = 60
)

func otpGroups() []Group {
	return []Group{{
		Name:    GroupOTP,
		Section: SectionAuthentication,
		Settings: []Definition{
			{Key: KeyOTPCodeTTLMinutes, Label: "Client Code Lifetime (minutes)", Type: TypeInt,
				Help:    "How long a code mailed to an app user stays valid. Admin codes keep their own lifetimes.",
				Default: func() any { return DefaultOTPCodeTTLMinutes }},
			{Key: KeyOTPResendCooldownSeconds, Label: "Resend Cooldown (seconds)", Type: TypeInt,
				Help:    "Minimum wait before another code can be mailed to the same address for the same purpose. Applies to app users and admins.",
				Default: func() any { return DefaultOTPResendCooldownSeconds }},
		},
		Validate: validateOTPGroup,
	}}
}

// validateOTPGroup rejects a cooldown that outlives the code: the user would
// hold an expired code with no way to ask for another.
func validateOTPGroup(effective map[string]string) error {
	ttl, err := strconv.Atoi(effective[KeyOTPCodeTTLMinutes])
	if err != nil || ttl < 1 || ttl > 60 {
		return fmt.Errorf("%s must be an integer between 1 and 60", KeyOTPCodeTTLMinutes)
	}
	cool, err := strconv.Atoi(effective[KeyOTPResendCooldownSeconds])
	if err != nil || cool < 15 || cool > 600 {
		return fmt.Errorf("%s must be an integer between 15 and 600", KeyOTPResendCooldownSeconds)
	}
	if cool >= ttl*60 {
		return fmt.Errorf("%s must be shorter than %s", KeyOTPResendCooldownSeconds, KeyOTPCodeTTLMinutes)
	}
	return nil
}
