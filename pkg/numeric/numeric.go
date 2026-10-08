// Package numeric holds exact decimals for PostgreSQL numeric columns (prices, USD
// values, multipliers). They read and write the column without float rounding and
// encode to JSON as a bare number, the form API clients have always received.
package numeric

import (
	"github.com/shopspring/decimal"
)

const jsonNull = "null"

// Decimal is a non-null numeric column.
type Decimal struct {
	decimal.Decimal
}

// NewDecimal wraps value.
func NewDecimal(value decimal.Decimal) Decimal {
	return Decimal{Decimal: value}
}

// MarshalJSON writes the exact digits as a JSON number (for example 1.25).
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.Decimal.String()), nil
}

// NullDecimal is a nullable numeric column: Valid is false for SQL NULL.
type NullDecimal struct {
	decimal.NullDecimal
}

// NewNullDecimal wraps a present (non-NULL) value.
func NewNullDecimal(value decimal.Decimal) NullDecimal {
	return NullDecimal{NullDecimal: decimal.NewNullDecimal(value)}
}

// NullDecimalFromPointer maps nil to SQL NULL.
func NullDecimalFromPointer(value *decimal.Decimal) NullDecimal {
	if value == nil {
		return NullDecimal{}
	}
	return NewNullDecimal(*value)
}

// MarshalJSON writes null for SQL NULL, otherwise the exact digits as a JSON number.
func (n NullDecimal) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte(jsonNull), nil
	}
	return []byte(n.Decimal.String()), nil
}

// IsZero reports whether n is the zero NullDecimal, which is SQL NULL; a stored 0 is
// not zero here. It lets `json:",omitzero"` drop NULL fields the way `omitempty`
// dropped nil pointers.
func (n NullDecimal) IsZero() bool {
	return !n.Valid
}

// Pointer returns nil for SQL NULL, otherwise a copy of the value.
func (n NullDecimal) Pointer() *decimal.Decimal {
	if !n.Valid {
		return nil
	}
	value := n.Decimal
	return &value
}
