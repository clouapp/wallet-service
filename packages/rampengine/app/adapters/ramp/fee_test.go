package ramp

import "testing"

func TestFeePercentToSpreadBps(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"whole percent", "1", 100},
		{"the stored format", "1.50", 150},
		{"zero", "0", 0},
		{"zero with places", "0.00", 0},
		{"the full bound", "100", 10000},
		{"sub-basis-point rounds half up", "0.005", 1},
		{"just under half rounds down", "0.0049", 0},
		{"exact half rounds up", "1.235", 124},
		// float64 computes 1.15*100 as 114.99999999999999, which truncates to
		// 114 and under-charges every order on the rail.
		{"the float trap", "1.15", 115},
		{"another float trap", "2.675", 268},
		{"surrounding whitespace", "  1.50  ", 150},
		// Well-formed decimals the old check accepted. The function also reads
		// stored values back, and one that stopped parsing would break quotes.
		{"no integer part", ".5", 50},
		{"trailing dot", "1.", 100},
		{"explicit plus", "+1.5", 150},
		{"explicit plus, no integer part", "+.5", 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FeePercentToSpreadBps(c.input)
			if err != nil {
				t.Fatalf("FeePercentToSpreadBps(%q) error: %v", c.input, err)
			}
			if got != c.want {
				t.Fatalf("FeePercentToSpreadBps(%q)=%d want %d", c.input, got, c.want)
			}
		})
	}
}

func TestFeePercentToSpreadBpsRejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"negative", "-1.00"},
		{"above one hundred", "100.01"},
		{"not a number", "abc"},
		// A fee is a decimal. Accepting a fraction would let a malformed
		// setting through as a plausible number.
		{"fraction", "3/4"},
		{"exponent", "1e2"},
		// big.Rat takes these too; none is a percent anybody typed.
		{"digit separator", "1_0"},
		{"hex float", "0x1p-2"},
		{"binary", "0b1"},
		{"two dots", "1.5.5"},
		{"a lone dot", "."},
		{"inner space", "1 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := FeePercentToSpreadBps(c.input); err == nil {
				t.Fatalf("FeePercentToSpreadBps(%q) accepted an invalid fee", c.input)
			}
		})
	}
}
