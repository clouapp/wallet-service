package auth

import (
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpPeriodSeconds = 30
	// totpSkewSteps is how many periods before and after now a code is still
	// accepted, the same tolerance totp.Validate applies.
	totpSkewSteps = 1
)

var totpStepOptions = totp.ValidateOpts{
	Period:    totpPeriodSeconds,
	Skew:      0,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

// MatchTOTP checks code against a plaintext secret and returns the time-step
// counter the code belongs to. The counter is what replay protection compares:
// a code is spent once its counter has been redeemed.
func (s *Service) MatchTOTP(secret, code string, at time.Time) (int64, bool) {
	if secret == "" || code == "" {
		return 0, false
	}
	currentStep := at.Unix() / totpPeriodSeconds
	for offset := int64(-totpSkewSteps); offset <= totpSkewSteps; offset++ {
		step := currentStep + offset
		stepStart := time.Unix(step*totpPeriodSeconds, 0).UTC()
		valid, err := totp.ValidateCustom(code, secret, stepStart, totpStepOptions)
		if err == nil && valid {
			return step, true
		}
	}
	return 0, false
}
