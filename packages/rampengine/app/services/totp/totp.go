// Package totp provides stateless RFC 6238 TOTP helpers and backup-code
// generation. It wraps github.com/pquerna/otp and holds no state, no DB, and
// no facades, so it is fully unit-testable and reusable across audiences.
package totp

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Params configures TOTP generation and validation.
type Params struct {
	Issuer string
	Digits otp.Digits
	Period uint
	Skew   uint
}

// GenerateSecret builds a new random TOTP key bound to accountName (the user's
// email). It returns the otpauth:// provisioning URI and the raw base32 secret.
func GenerateSecret(p Params, accountName string) (uri string, secret string, err error) {
	p = normalizeParams(p)
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      p.Issuer,
		AccountName: accountName,
		Period:      p.Period,
		Digits:      p.Digits,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return key.URL(), key.Secret(), nil
}

func normalizeParams(p Params) Params {
	if p.Period == 0 {
		p.Period = 30
	}
	if p.Skew == 0 {
		p.Skew = 1
	}
	return p
}

// Validate checks code against secret at instant t, honoring p.Skew windows on
// each side. On a match it returns the matched window counter so the caller
// can persist it for replay protection. A code of the wrong length is wrong,
// not an error: it is a typo.
func Validate(p Params, secret, code string, t time.Time) (counter int64, valid bool, err error) {
	p = normalizeParams(p)
	if len(code) != p.Digits.Length() {
		return 0, false, nil
	}
	baseCounter := t.Unix() / int64(p.Period)
	for offset := -int64(p.Skew); offset <= int64(p.Skew); offset++ {
		c := baseCounter + offset
		windowTime := time.Unix(c*int64(p.Period), 0)
		ok, valErr := totp.ValidateCustom(code, secret, windowTime, totp.ValidateOpts{
			Period:    p.Period,
			Skew:      0,
			Digits:    p.Digits,
			Algorithm: otp.AlgorithmSHA1,
		})
		if valErr != nil {
			return 0, false, fmt.Errorf("totp: validate: %w", valErr)
		}
		if ok {
			return c, true, nil
		}
	}
	return 0, false, nil
}

// backupAlphabet excludes ambiguous characters (0/O, 1/I) for readability.
const backupAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// A backup code is backupCodeLength characters of backupAlphabet, written with
// a dash after the first backupCodeSplit: "XXXX-XXXX".
const (
	backupCodeLength = 8
	backupCodeSplit  = 4
)

// GenerateBackupCodes returns n unique single-use recovery codes formatted as
// "XXXX-XXXX" using a non-ambiguous alphabet.
func GenerateBackupCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	seen := make(map[string]struct{}, n)
	for len(codes) < n {
		code, err := randomBackupCode()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

func randomBackupCode() (string, error) {
	buf := make([]byte, backupCodeLength)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(backupAlphabet))))
		if err != nil {
			return "", fmt.Errorf("totp: backup code: %w", err)
		}
		buf[i] = backupAlphabet[idx.Int64()]
	}
	return formatBackupCode(string(buf)), nil
}

// formatBackupCode splits the characters of a code 4-4 around the dash.
func formatBackupCode(chars string) string {
	return chars[:backupCodeSplit] + "-" + chars[backupCodeSplit:]
}

// NormalizeBackupCode reads a typed backup code back into the shape
// GenerateBackupCodes wrote, which is the shape its hash was taken of. Case,
// spaces and dashes are how a person copies a code, not part of it. ok is false
// for anything that is not exactly backupCodeLength characters of the
// alphabet: that is no code, and the caller answers it as a wrong one.
func NormalizeBackupCode(typed string) (code string, ok bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == ' ' || r == '-':
			continue
		case r > unicode.MaxASCII || !strings.ContainsRune(backupAlphabet, r):
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != backupCodeLength {
		return "", false
	}
	return formatBackupCode(b.String()), true
}
