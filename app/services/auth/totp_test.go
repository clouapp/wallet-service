package auth_test

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const totpPeriod = 30

func newTOTPSecret(t *testing.T) string {
	t.Helper()
	secret, _, err := authsvc.NewService().GenerateTOTP("user@example.com")
	require.NoError(t, err)
	return secret
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	require.NoError(t, err)
	return code
}

func TestMatch_TOTP_ReturnsTheStepOfTheCode(t *testing.T) {
	secret := newTOTPSecret(t)
	now := time.Unix(1_700_000_010, 0)

	step, ok := authsvc.NewService().MatchTOTP(secret, codeAt(t, secret, now), now)

	require.True(t, ok)
	require.Equal(t, now.Unix()/totpPeriod, step)
}

func TestMatch_TOTP_AcceptsTheAdjacentStepsAndReportsWhichOne(t *testing.T) {
	secret := newTOTPSecret(t)
	now := time.Unix(1_700_000_010, 0)
	previous := now.Add(-totpPeriod * time.Second)
	next := now.Add(totpPeriod * time.Second)

	step, ok := authsvc.NewService().MatchTOTP(secret, codeAt(t, secret, previous), now)
	require.True(t, ok)
	require.Equal(t, previous.Unix()/totpPeriod, step)

	step, ok = authsvc.NewService().MatchTOTP(secret, codeAt(t, secret, next), now)
	require.True(t, ok)
	require.Equal(t, next.Unix()/totpPeriod, step)
}

func TestMatch_TOTP_RefusesCodesOutsideTheSkew(t *testing.T) {
	secret := newTOTPSecret(t)
	now := time.Unix(1_700_000_010, 0)
	stale := now.Add(-3 * totpPeriod * time.Second)

	_, ok := authsvc.NewService().MatchTOTP(secret, codeAt(t, secret, stale), now)

	require.False(t, ok)
}

func TestMatch_TOTP_RefusesEmptyInputAndTheSealedSecret(t *testing.T) {
	secret := newTOTPSecret(t)
	now := time.Unix(1_700_000_010, 0)
	code := codeAt(t, secret, now)
	svc := authsvc.NewService()

	_, ok := svc.MatchTOTP("", code, now)
	require.False(t, ok)
	_, ok = svc.MatchTOTP(secret, "", now)
	require.False(t, ok)
	_, ok = svc.MatchTOTP("sealed:"+secret, code, now)
	require.False(t, ok, "a code never matches the ciphertext of the secret")
}
