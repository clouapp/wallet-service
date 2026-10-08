package numeric

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// ErrOutOfRange marks a value that does not fit its numeric(precision, scale) column.
var ErrOutOfRange = errors.New("value does not fit the numeric column")

// ErrTooManyDecimals marks a value with more fractional digits than its column keeps.
var ErrTooManyDecimals = errors.New("value has more decimal places than the numeric column keeps")

// ErrNotPositive marks a zero or negative value where only positive values are allowed.
var ErrNotPositive = errors.New("value must be greater than zero")

// ErrNegative marks a negative value where only zero or more is allowed.
var ErrNegative = errors.New("value must not be negative")

// Column describes a PostgreSQL numeric(Precision, Scale) column.
type Column struct {
	Name      string
	Precision int32
	Scale     int32
}

// Validate accepts value only when it fits the column exactly: no more than Scale
// decimal places (trailing zeros do not count) and no more than Precision-Scale
// integer digits. Use it on user input, where silent rounding would change intent.
func (c Column) Validate(value decimal.Decimal) error {
	if err := c.requireDefined(); err != nil {
		return err
	}
	if !value.Equal(value.Round(c.Scale)) {
		return fmt.Errorf("%s %s: %w (%d)", c.Name, value.String(), ErrTooManyDecimals, c.Scale)
	}
	return c.requireIntegerDigitsFit(value)
}

// Fit rounds value half away from zero to the column scale (as PostgreSQL does on
// write) and rejects it when the integer part overflows. Use it on values computed or
// quoted elsewhere (prices), so what is cached matches what is stored.
func (c Column) Fit(value decimal.Decimal) (decimal.Decimal, error) {
	if err := c.requireDefined(); err != nil {
		return decimal.Decimal{}, err
	}
	rounded := value.Round(c.Scale)
	if err := c.requireIntegerDigitsFit(rounded); err != nil {
		return decimal.Decimal{}, err
	}
	return rounded, nil
}

// ParsePositive parses text that must be a positive number fitting the column exactly.
func (c Column) ParsePositive(text string) (decimal.Decimal, error) {
	value, err := Parse(c.Name, text)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !value.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("%s %s: %w", c.Name, value.String(), ErrNotPositive)
	}
	if err := c.Validate(value); err != nil {
		return decimal.Decimal{}, err
	}
	return value, nil
}

func (c Column) requireDefined() error {
	if strings.TrimSpace(c.Name) == "" || c.Precision <= 0 || c.Scale < 0 || c.Scale > c.Precision {
		return fmt.Errorf("numeric column %q has an invalid definition numeric(%d,%d)", c.Name, c.Precision, c.Scale)
	}
	return nil
}

func (c Column) requireIntegerDigitsFit(value decimal.Decimal) error {
	limit := decimal.New(1, c.Precision-c.Scale)
	if value.Abs().Cmp(limit) >= 0 {
		return fmt.Errorf("%s %s: %w numeric(%d,%d)", c.Name, value.String(), ErrOutOfRange, c.Precision, c.Scale)
	}
	return nil
}

// Parse reads a decimal number written in plain or exponent notation (1.25, 1e-3).
// Empty text, NaN, infinities and hexadecimal floats are rejected.
func Parse(name, text string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return decimal.Decimal{}, fmt.Errorf("%s is empty", name)
	}
	value, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%s %q is not a decimal number", name, trimmed)
	}
	return value, nil
}

// ParseNonNegative is Parse that also rejects negative values.
func ParseNonNegative(name, text string) (decimal.Decimal, error) {
	value, err := Parse(name, text)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if value.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("%s %s: %w", name, value.String(), ErrNegative)
	}
	return value, nil
}
