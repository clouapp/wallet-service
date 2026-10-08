package amount

import (
	"errors"
	"strings"
	"testing"
)

func TestSign_Is_Negative(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"":           false,
		"0":          false,
		"5000000000": false,
		"0.02":       false,
		"-5":         true,
		" -0.02 ":    true,
		"-0":         true,
		"--1":        true,
		"1-2":        false,
	}
	for value, want := range cases {
		if got := IsNegative(value); got != want {
			t.Errorf("IsNegative(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRequire_Non_Negative(t *testing.T) {
	t.Parallel()

	if err := RequireNonNegative("transactions.amount", "5000000000"); err != nil {
		t.Fatalf("positive amount: %v", err)
	}
	if err := RequireNonNegative("transactions.fee", ""); err != nil {
		t.Fatalf("unset optional amount: %v", err)
	}
	err := RequireNonNegative("transactions.amount", "-5000000000")
	if !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative amount error = %v, want ErrNegativeAmount", err)
	}
	if want := `transactions.amount "-5000000000" rejected`; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("error %q does not name the column and value", err)
	}
	if err := RequireNonNegative(" ", "1"); err == nil {
		t.Fatal("blank column name accepted")
	}
}

func TestRequire_Non_NegativeColumns(t *testing.T) {
	t.Parallel()

	negative := "-1"
	var missing *string
	cases := map[string]struct {
		fields  map[string]any
		wantErr bool
	}{
		"no amount columns":       {fields: map[string]any{"status": "confirmed"}},
		"positive string":         {fields: map[string]any{"amount": "10"}},
		"nil value":               {fields: map[string]any{"amount": nil}},
		"nil string pointer":      {fields: map[string]any{"amount": missing}},
		"negative string":         {fields: map[string]any{"amount": "-10"}, wantErr: true},
		"negative string pointer": {fields: map[string]any{"fee": &negative}, wantErr: true},
		"negative float":          {fields: map[string]any{"fee": -0.5}, wantErr: true},
		"negative int":            {fields: map[string]any{"amount": -3}, wantErr: true},
		"unlisted negative":       {fields: map[string]any{"confirmations": -1}},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := RequireNonNegativeColumns(tc.fields, "amount", "fee")
			if tc.wantErr != (err != nil) {
				t.Fatalf("RequireNonNegativeColumns(%v) error = %v, wantErr %v", tc.fields, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrNegativeAmount) {
				t.Fatalf("error %v is not ErrNegativeAmount", err)
			}
		})
	}
}
