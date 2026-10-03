package numeric

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

var feeMultiplier = Column{Name: "fee_multiplier", Precision: 8, Scale: 4}

func TestValidateAcceptsValuesThatFitExactly(t *testing.T) {
	for _, text := range []string{"0.0001", "1.25", "1.25000000", "9999.9999", "-9999.9999", "0"} {
		if err := feeMultiplier.Validate(mustParse(t, text)); err != nil {
			t.Fatalf("validate %s: %v", text, err)
		}
	}
}

func TestValidateRejectsExtraDecimalsInsteadOfRounding(t *testing.T) {
	if err := feeMultiplier.Validate(mustParse(t, "1.23456")); !errors.Is(err, ErrTooManyDecimals) {
		t.Fatalf("err = %v, want ErrTooManyDecimals", err)
	}
}

func TestValidateRejectsIntegerOverflow(t *testing.T) {
	for _, text := range []string{"10000", "-10000", "12345.6"} {
		if err := feeMultiplier.Validate(mustParse(t, text)); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("validate %s: err = %v, want ErrOutOfRange", text, err)
		}
	}
}

func TestFitRoundsHalfAwayFromZeroLikePostgres(t *testing.T) {
	for text, want := range map[string]string{"1.23455": "1.2346", "-1.23455": "-1.2346", "1.23454": "1.2345", "2": "2"} {
		got, err := feeMultiplier.Fit(mustParse(t, text))
		if err != nil {
			t.Fatalf("fit %s: %v", text, err)
		}
		if !got.Equal(decimal.RequireFromString(want)) {
			t.Fatalf("fit %s = %s, want %s", text, got, want)
		}
	}
}

func TestFitRejectsAValueThatOverflowsAfterRounding(t *testing.T) {
	if _, err := feeMultiplier.Fit(mustParse(t, "9999.99995")); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
}

func TestParsePositive(t *testing.T) {
	got, err := feeMultiplier.ParsePositive(" 1.5 ")
	if err != nil || !got.Equal(mustParse(t, "1.5")) {
		t.Fatalf("got %v, %v", got, err)
	}
	for text, want := range map[string]error{"0": ErrNotPositive, "-1": ErrNotPositive, "1.00001": ErrTooManyDecimals, "10000": ErrOutOfRange} {
		if _, err := feeMultiplier.ParsePositive(text); !errors.Is(err, want) {
			t.Fatalf("parse %s: err = %v, want %v", text, err, want)
		}
	}
	if _, err := feeMultiplier.ParsePositive("NaN"); err == nil {
		t.Fatal("NaN must be rejected")
	}
}

func TestInvalidColumnDefinitionsAreRejected(t *testing.T) {
	for _, column := range []Column{{Name: "", Precision: 8, Scale: 4}, {Name: "x", Precision: 0, Scale: 0}, {Name: "x", Precision: 4, Scale: 8}, {Name: "x", Precision: 8, Scale: -1}} {
		if err := column.Validate(decimal.Zero); err == nil {
			t.Fatalf("column %+v: expected a definition error", column)
		}
		if _, err := column.Fit(decimal.Zero); err == nil {
			t.Fatalf("column %+v: expected a definition error", column)
		}
	}
}
