package amount

import (
	"math/big"
	"testing"
)

func TestFormat_Base_Units(t *testing.T) {
	cases := []struct {
		units    string
		decimals int
		want     string
	}{
		{"25000000", 6, "25"},
		{"3000000", 6, "3"},
		{"1", 6, "0.000001"},
		{"500000000000000000", 18, "0.5"},
		{"123456789012345678", 18, "0.123456789012345678"},
		{"1", 18, "0.000000000000000001"},
		{"1000000000000000000000", 18, "1000"},
		{"150000000", 8, "1.5"},
		{"1", 8, "0.00000001"},
		{"2500000000", 9, "2.5"},
		{"0", 6, "0"},
		{"42", 0, "42"},
		{"-1500000", 6, "-1.5"},
	}
	for _, c := range cases {
		units, ok := new(big.Int).SetString(c.units, 10)
		if !ok {
			t.Fatalf("bad fixture %q", c.units)
		}
		if got := FormatBaseUnits(units, c.decimals); got != c.want {
			t.Errorf("FormatBaseUnits(%s, %d) = %q, want %q", c.units, c.decimals, got, c.want)
		}
	}
	if got := FormatBaseUnits(nil, 6); got != "" {
		t.Errorf("FormatBaseUnits(nil) = %q, want empty", got)
	}
}

func TestAmount_Normalize_Decimal(t *testing.T) {
	cases := map[string]string{"3.000": "3", "0.50": "0.5", "25": "25", " 1.10 ": "1.1", "": ""}
	for in, want := range cases {
		if got := NormalizeDecimal(in); got != want {
			t.Errorf("NormalizeDecimal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecimal_To_BaseUnits(t *testing.T) {
	cases := []struct {
		value    string
		decimals int
		want     string
	}{
		{"1.5", 8, "150000000"},
		{"1.5", 2, "150"},
		{"0.00000001", 8, "1"},
		{"0", 18, "0"},
		{" 3 ", 0, "3"},
		{"25000000", 0, "25000000"},
	}
	for _, c := range cases {
		got, err := DecimalToBaseUnits(c.value, c.decimals)
		if err != nil || got.String() != c.want {
			t.Errorf("DecimalToBaseUnits(%q, %d) = %v, %v; want %s", c.value, c.decimals, got, err, c.want)
		}
	}
	for _, bad := range []struct {
		value    string
		decimals int
	}{
		{"-1", 8},
		{"1.5", 0},
		{"1e6", 6},
		{"", 8},
		{"abc", 8},
	} {
		if _, err := DecimalToBaseUnits(bad.value, bad.decimals); err == nil {
			t.Errorf("DecimalToBaseUnits(%q, %d) must fail", bad.value, bad.decimals)
		}
	}
}

func TestParse_Base_Units(t *testing.T) {
	if units, ok := ParseBaseUnits(" 25000000 "); !ok || units.String() != "25000000" {
		t.Fatalf("ParseBaseUnits(25000000) = %v, %v", units, ok)
	}
	for _, bad := range []string{"", "25.5", "-1", "abc", "1e6"} {
		if _, ok := ParseBaseUnits(bad); ok {
			t.Errorf("ParseBaseUnits(%q) must fail", bad)
		}
	}
}
