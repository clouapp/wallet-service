package numeric

import (
	"errors"
	"testing"
)

func TestTo_Base_UnitsIsExactWhereFloatMultiplicationTruncates(t *testing.T) {
	cases := []struct {
		human    string
		decimals int32
		want     string
	}{
		{"0.29", 8, "29000000"},
		{"0.1", 18, "100000000000000000"},
		{"1.5", 6, "1500000"},
		{"123456789.123456789123456789", 18, "123456789123456789123456789"},
		{"0", 18, "0"},
		{"7", 0, "7"},
	}
	for _, c := range cases {
		got, err := ToBaseUnits(mustParse(t, c.human), c.decimals)
		if err != nil {
			t.Fatalf("%s × 10^%d: %v", c.human, c.decimals, err)
		}
		if got.String() != c.want {
			t.Fatalf("%s × 10^%d = %s, want %s", c.human, c.decimals, got, c.want)
		}
	}
}

func TestTo_Base_UnitsRoundsProviderNoiseToTheNearestUnit(t *testing.T) {
	for human, want := range map[string]string{"0.30000000000000004": "300000", "0.29999999999999998": "300000", "0.0000005": "1"} {
		got, err := ToBaseUnits(mustParse(t, human), 6)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want {
			t.Fatalf("%s = %s, want %s", human, got, want)
		}
	}
}

func TestTo_Base_UnitsRejectsBadInput(t *testing.T) {
	if _, err := ToBaseUnits(mustParse(t, "-1"), 8); !errors.Is(err, ErrNegative) {
		t.Fatalf("err = %v, want ErrNegative", err)
	}
	for _, decimals := range []int32{-1, MaxBaseUnitDecimals + 1} {
		if _, err := ToBaseUnits(mustParse(t, "1"), decimals); err == nil {
			t.Fatalf("decimals %d: expected an error", decimals)
		}
	}
}
