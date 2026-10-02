package amount

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNegativeAmount marks a write that would store a signed amount. Stored amounts
// are always absolute; the direction of a movement comes from its transaction type.
var ErrNegativeAmount = errors.New("amounts are stored as absolute values; the transaction type carries the direction")

const negativeSign = "-"

// IsNegative reports whether a stored decimal or base-unit string carries a minus sign.
func IsNegative(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), negativeSign)
}

// RequireNonNegative rejects a negative value of the named column. Empty values pass:
// optional columns (fee, fee_estimate) are validated only when set.
func RequireNonNegative(column, value string) error {
	if strings.TrimSpace(column) == "" {
		return errors.New("amount column name is required")
	}
	if IsNegative(value) {
		return fmt.Errorf("%s %q rejected: %w", column, strings.TrimSpace(value), ErrNegativeAmount)
	}
	return nil
}

// RequireNonNegativeColumns checks every listed column present in an update map.
// Values that are not strings are formatted with %v, so numeric types are covered too.
func RequireNonNegativeColumns(fields map[string]any, columns ...string) error {
	for _, column := range columns {
		value, present := fields[column]
		if !present || value == nil {
			continue
		}
		if err := RequireNonNegative(column, fmt.Sprint(dereference(value))); err != nil {
			return err
		}
	}
	return nil
}

func dereference(value any) any {
	switch typed := value.(type) {
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	case *float64:
		if typed == nil {
			return ""
		}
		return *typed
	default:
		return value
	}
}
