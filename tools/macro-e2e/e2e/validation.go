package e2e

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	MaxDecimals           = 36
	IdempotencyNamespace  = "macro-e2e-funding:"
	decimalPoint          = "."
	pythonReprQuote       = "'"
	pythonReprDoubleQuote = `"`
)

var (
	UUIDPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	AssetPattern          = regexp.MustCompile(`^[A-Z0-9]{2,12}$`)
	AmountPattern         = regexp.MustCompile(`^[1-9][0-9]{0,40}$`)
	AddressPattern        = regexp.MustCompile(`^[A-Za-z0-9]{20,100}$`)
	TagPattern            = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,80}$`)
	ExternalUserPattern   = regexp.MustCompile(`^[0-9]{1,12}$`)
	RecordingOwnerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,80}$`)
	ChainPattern          = regexp.MustCompile(`^[a-z0-9]{2,20}$`)
)

// Require returns value when it matches pattern, else an "invalid <what>" error.
func Require(pattern *regexp.Regexp, value, what string) (string, error) {
	if !pattern.MatchString(value) {
		return "", fmt.Errorf("invalid %s: %s", what, pythonRepr(value))
	}
	return value, nil
}

type fieldCheck struct {
	pattern *regexp.Regexp
	value   string
	what    string
}

func requireAll(checks []fieldCheck) error {
	for _, check := range checks {
		if _, err := Require(check.pattern, check.value, check.what); err != nil {
			return err
		}
	}
	return nil
}

// pythonRepr quotes like Python's repr(str) (used in error messages only).
func pythonRepr(value string) string {
	quote := pythonReprQuote
	if strings.Contains(value, pythonReprQuote) && !strings.Contains(value, pythonReprDoubleQuote) {
		quote = pythonReprDoubleQuote
	}
	quoted := strconv.Quote(value)
	inner := quoted[1 : len(quoted)-1]
	inner = strings.ReplaceAll(inner, `\"`, `"`)
	if quote == pythonReprQuote {
		inner = strings.ReplaceAll(inner, pythonReprQuote, `\'`)
	}
	return quote + inner + quote
}

// ParseDecimals parses the token decimals (0..36).
func ParseDecimals(raw string) (int, error) {
	decimals, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid decimals %q: not an integer", raw)
	}
	if decimals < 0 || decimals > MaxDecimals {
		return 0, fmt.Errorf("invalid decimals %d", decimals)
	}
	return decimals, nil
}

// HumanAmount scales base units down by decimals, keeping every fractional digit, like
// Python's format(Decimal(base_units).scaleb(-decimals), "f") but exact beyond 28 digits.
func HumanAmount(baseUnits string, decimals int) (string, error) {
	if !AmountPattern.MatchString(baseUnits) {
		return "", fmt.Errorf("invalid amount (base units): %s", pythonRepr(baseUnits))
	}
	if decimals < 0 || decimals > MaxDecimals {
		return "", fmt.Errorf("invalid decimals %d", decimals)
	}
	if _, ok := new(big.Int).SetString(baseUnits, 10); !ok {
		return "", fmt.Errorf("invalid amount (base units): %s", pythonRepr(baseUnits))
	}
	if decimals == 0 {
		return baseUnits, nil
	}
	padded := baseUnits
	if len(padded) <= decimals {
		padded = strings.Repeat("0", decimals+1-len(padded)) + padded
	}
	split := len(padded) - decimals
	return padded[:split] + decimalPoint + padded[split:], nil
}

// IdempotencyKey is the UUIDv5 (URL namespace) of "macro-e2e-funding:<tag>".
func IdempotencyKey(tag string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(IdempotencyNamespace+tag)).String()
}
