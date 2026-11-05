package ramp

import (
	"math/big"
	"testing"
)

func TestFeeAmount(t *testing.T) {
	cases := []struct {
		name       string
		amount     string
		feePercent string
		scale      int
		want       string
	}{
		{"one percent of a round amount", "100.00", "1", 2, "1.00"},
		{"the stored fee format", "250.00", "1.50", 2, "3.75"},
		{"zero fee still carries the scale", "100.00", "0", 2, "0.00"},
		{"crypto scale", "18.500000", "1.50", 6, "0.277500"},
		// A fee is money: 0.07 * 3 in binary floating point is
		// 0.21000000000000002, and the same class of error compounds here.
		{"the float trap", "1150.00", "1.15", 2, "13.23"},
		{"half rounds up, never toward the house at random", "0.05", "50", 2, "0.03"},
		{"whitespace is tolerated", " 100.00 ", " 1.50 ", 2, "1.50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FeeAmount(c.amount, c.feePercent, c.scale)
			if err != nil {
				t.Fatalf("FeeAmount(%q, %q) error: %v", c.amount, c.feePercent, err)
			}
			if got != c.want {
				t.Fatalf("FeeAmount(%q, %q) = %q, want %q", c.amount, c.feePercent, got, c.want)
			}
		})
	}
}

func TestConvertAmount(t *testing.T) {
	cases := []struct {
		name         string
		amount, rate string
		scale        int
		want         string
	}{
		{"brl to a stablecoin", "100.00", "0.185000", 6, "18.500000"},
		{"the inverse direction", "18.500000", "5.405405", 2, "100.00"},
		{"rate of one is identity", "42.00", "1", 2, "42.00"},
		{"zero amount", "0", "5.40", 2, "0.00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ConvertAmount(c.amount, c.rate, c.scale)
			if err != nil {
				t.Fatalf("ConvertAmount(%q, %q) error: %v", c.amount, c.rate, err)
			}
			if got != c.want {
				t.Fatalf("ConvertAmount(%q, %q) = %q, want %q", c.amount, c.rate, got, c.want)
			}
		})
	}
}

func TestSubtractAmount(t *testing.T) {
	got, err := SubtractAmount("100.00", "1.50", 2)
	if err != nil {
		t.Fatalf("SubtractAmount error: %v", err)
	}
	if got != "98.50" {
		t.Fatalf("SubtractAmount = %q, want %q", got, "98.50")
	}

	// A fee larger than the amount is a misconfigured rail, not a negative
	// order. Refusing here means it surfaces as an error rather than as a
	// transfer that pays out less than nothing.
	if _, err := SubtractAmount("1.00", "2.00", 2); err == nil {
		t.Fatal("SubtractAmount must reject a result below zero")
	}
}

func TestMoneyRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name         string
		amount, rate string
	}{
		{"empty amount", "", "1"},
		{"empty rate", "1", ""},
		{"not a number", "abc", "1"},
		// big.Rat parses both of these, and a malformed setting would slip
		// through as a plausible number.
		{"a fraction", "3/4", "1"},
		{"scientific notation", "1e2", "1"},
		{"negative amount", "-1.00", "1"},
		{"negative rate", "1.00", "-1"},
		// big.Rat's grammar is wider than it looks. Each of these parses to a
		// plausible number nobody typed as an amount, which is why the check is
		// an allow-list and not a list of characters to refuse.
		{"binary exponent", "1p10", "1"},         // 1024
		{"hex float", "0x1p10", "1"},             // 1024
		{"binary literal", "0b101p2", "1"},       // 20
		{"digit separators", "1_000", "1"},       // 1000
		{"explicit plus", "+5", "1"},             // 5
		{"trailing dot", "5.", "1"},              // 5
		{"leading dot", ".5", "1"},               // 0.5
		{"binary exponent in rate", "1", "1p10"}, // 1024
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ConvertAmount(c.amount, c.rate, 2); err == nil {
				t.Fatalf("ConvertAmount(%q, %q) must fail", c.amount, c.rate)
			}
		})
	}

	if _, err := FeeAmount("100.00", "101", 2); err == nil {
		t.Fatal("a fee percent above 100 must fail")
	}
	if _, err := FeeAmount("100.00", "1.00", -1); err == nil {
		t.Fatal("a negative scale must fail")
	}
}

func TestDivideAmount(t *testing.T) {
	// Bridge quotes usd→brl only, so an on-ramp of 100 BRL at 5.45 divides.
	got, err := DivideAmount("100.00", "5.45", 6)
	if err != nil {
		t.Fatalf("DivideAmount error: %v", err)
	}
	if got != "18.348624" {
		t.Fatalf("DivideAmount = %q, want %q", got, "18.348624")
	}

	// Inverting first and multiplying second rounds twice. The direct quotient
	// is what the user is quoted, so it must not go through an inverted rate.
	inverted, err := DivideAmount("1", "5.45", 6)
	if err != nil {
		t.Fatalf("DivideAmount error: %v", err)
	}
	twoStep, err := ConvertAmount("100.00", inverted, 6)
	if err != nil {
		t.Fatalf("ConvertAmount error: %v", err)
	}
	if twoStep == got {
		t.Fatalf("the two-step path happened to agree (%q); the test no longer proves anything", twoStep)
	}

	if _, err := DivideAmount("100.00", "0", 2); err == nil {
		t.Fatal("dividing by a zero rate must fail")
	}
}

func TestFloorAmount(t *testing.T) {
	cases := []struct {
		name   string
		amount string
		scale  int
		want   string
	}{
		// A whole wallet balance, as the app sends it, against a provider that
		// moves two places.
		{"a balance at the token's own scale", "3.753027", 2, "3.75"},
		// Down, never to nearest: the user cannot deposit a cent they do not have.
		{"never rounds up", "3.759999", 2, "3.75"},
		{"fewer places are padded", "3.7", 2, "3.70"},
		{"an integer", "3", 2, "3.00"},
		{"trailing zeros past the scale", "3.7500", 2, "3.75"},
		{"whitespace is tolerated", " 3.753027 ", 2, "3.75"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FloorAmount(c.amount, c.scale)
			if err != nil {
				t.Fatalf("FloorAmount(%q, %d) error: %v", c.amount, c.scale, err)
			}
			if got != c.want {
				t.Fatalf("FloorAmount(%q, %d) = %q, want %q", c.amount, c.scale, got, c.want)
			}
		})
	}

	if _, err := FloorAmount("1e2", 2); err == nil {
		t.Fatal("FloorAmount must reject a non-decimal")
	}
	if _, err := FloorAmount("3.75", -1); err == nil {
		t.Fatal("FloorAmount must reject a negative scale")
	}
}

func TestSourceForMinimum(t *testing.T) {
	cases := []struct {
		name                           string
		source, receive, targetReceive string
		receiveScale, scale            int
		want                           string
	}{
		// The rail minimum expressed in what the user actually types: 2 USDC
		// quoted at R$ 10.94, and the R$ 20.00 floor needs 3.66 of them.
		{"the rail minimum as a token amount", "2", "10.94", "20.00", 2, 2, "3.66"},
		// 2 tokens at 4.9975 produce 9.995, shown as 10.00. The exact ratio
		// says 4.00, and 4.00 re-quotes to R$ 19.99 — a cent under the floor.
		{"an exact ratio is still nudged clear of the bound", "2", "10.00", "20.00", 2, 2, "4.01"},
		{"a hair over rounds up, not to nearest", "1", "10.00", "30.01", 2, 2, "3.01"},
		{"the scale is honoured", "2", "10.94", "20.00", 2, 6, "3.657979"},
		{"target equal to receive is not identity", "7.50", "41.00", "41.00", 2, 2, "7.51"},
		{"whitespace is tolerated", " 2 ", " 10.94 ", " 20.00 ", 2, 2, "3.66"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QuotedAmounts{Source: c.source, Receive: c.receive, ReceiveScale: c.receiveScale}
			got, err := SourceForMinimum(q, c.targetReceive, c.scale)
			if err != nil {
				t.Fatalf("SourceForMinimum(%+v, %q) error: %v", q, c.targetReceive, err)
			}
			if got != c.want {
				t.Fatalf("SourceForMinimum(%+v, %q) = %q, want %q", q, c.targetReceive, got, c.want)
			}
		})
	}
}

func TestSourceForMaximum(t *testing.T) {
	cases := []struct {
		name                           string
		source, receive, targetReceive string
		receiveScale, scale            int
		want                           string
	}{
		// The mirror of the minimum: 10 USDC quoted at R$ 54.70 against a
		// R$ 50.00 cap fits 9.13 of them, rounded DOWN.
		{"the rail cap as a token amount", "10", "54.70", "50.00", 2, 2, "9.13"},
		{"a hair over the cap comes back under it", "10", "50.01", "50.00", 2, 2, "9.99"},
		{"an exact ratio is still nudged clear of the bound", "2", "10.00", "20.00", 2, 2, "3.99"},
		{"the scale is honoured", "10", "54.70", "50.00", 2, 6, "9.139932"},
		{"target equal to receive is not identity", "7.50", "41.00", "41.00", 2, 2, "7.49"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QuotedAmounts{Source: c.source, Receive: c.receive, ReceiveScale: c.receiveScale}
			got, err := SourceForMaximum(q, c.targetReceive, c.scale)
			if err != nil {
				t.Fatalf("SourceForMaximum(%+v, %q) error: %v", q, c.targetReceive, err)
			}
			if got != c.want {
				t.Fatalf("SourceForMaximum(%+v, %q) = %q, want %q", q, c.targetReceive, got, c.want)
			}
		})
	}
}

// receive arrives rounded to the fiat scale, and the proportion multiplies
// that error by target/receive. On a small quote the factor is large enough to
// swallow a fixed slack, so the slack has to live inside the division. Every
// row here is a case a one-unit slack answers wrong.
func TestSourceForMinimumClearsTheBoundOnSmallQuotes(t *testing.T) {
	cases := []struct {
		name            string
		source, receive string
		want            string
	}{
		{"five cents of a token at 5.10", "0.05", "0.26", "3.93"},
		{"a tenth of a token at 5.05", "0.1", "0.51", "3.97"},
		{"a fifth of a token at 5.1250", "0.2", "1.03", "3.91"},
		{"five cents of a token behind a 1.5% fee", "0.05", "0.25", "4.09"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QuotedAmounts{Source: c.source, Receive: c.receive, ReceiveScale: 2}
			got, err := SourceForMinimum(q, "20.00", 2)
			if err != nil {
				t.Fatalf("SourceForMinimum error: %v", err)
			}
			if got != c.want {
				t.Fatalf("SourceForMinimum = %q, want %q", got, c.want)
			}
		})
	}
}

// A cap low enough to floor to nothing has no amount to suggest. Failing is
// how the caller learns to drop the token figure instead of telling the user
// to type zero.
func TestSourceForMaximumRefusesACapThatFloorsToZero(t *testing.T) {
	q := QuotedAmounts{Source: "10", Receive: "54.70", ReceiveScale: 2}
	if _, err := SourceForMaximum(q, "0.05", 2); err == nil {
		t.Fatal("a cap that floors to zero must fail rather than suggest 0.00")
	}
}

func TestSourceForBoundsRefuseUnusableInput(t *testing.T) {
	cases := []struct {
		name          string
		q             QuotedAmounts
		targetReceive string
		scale         int
	}{
		// A quote that produced nothing carries no proportion to scale.
		{"a zero receive", QuotedAmounts{"2", "0", 2}, "20.00", 2},
		{"a zero receive written with decimals", QuotedAmounts{"2", "0.00", 2}, "20.00", 2},
		{"a non-decimal source", QuotedAmounts{"1e2", "10.00", 2}, "20.00", 2},
		{"a negative scale", QuotedAmounts{"2", "10.00", 2}, "20.00", -1},
		{"a negative receive scale", QuotedAmounts{"2", "10.00", -1}, "20.00", 2},
		// 0.005 is entirely rounding at scale 2: nothing is left to scale.
		{"a receive inside its own rounding interval", QuotedAmounts{"2", "0.005", 2}, "20.00", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SourceForMinimum(c.q, c.targetReceive, c.scale); err == nil {
				t.Fatalf("SourceForMinimum(%+v, %q, %d) must fail", c.q, c.targetReceive, c.scale)
			}
		})
	}
}

// The suggested amount has to clear the bar it was derived from. Rounding it
// down would produce a number the API rejects on the very next request, which
// is the failure this helper exists to prevent.
func TestSourceForMinimumSuggestionClearsTheTarget(t *testing.T) {
	const source, receive, target = "2", "10.94", "20.00"

	suggested, err := SourceForMinimum(
		QuotedAmounts{Source: source, Receive: receive, ReceiveScale: 2}, target, 2)
	if err != nil {
		t.Fatalf("SourceForMinimum error: %v", err)
	}

	// Replay the quote's own proportion on the suggestion: receive / source
	// per unit, times the suggested amount.
	perUnit, err := DivideAmount(receive, source, 18)
	if err != nil {
		t.Fatalf("DivideAmount error: %v", err)
	}
	produced, err := ConvertAmount(suggested, perUnit, 2)
	if err != nil {
		t.Fatalf("ConvertAmount error: %v", err)
	}

	got, ok := new(big.Rat).SetString(produced)
	if !ok {
		t.Fatalf("could not parse produced amount %q", produced)
	}
	want, ok := new(big.Rat).SetString(target)
	if !ok {
		t.Fatalf("could not parse target %q", target)
	}
	if got.Cmp(want) < 0 {
		t.Fatalf("suggesting %q produces %q, which is below the %q target", suggested, produced, target)
	}
}

func TestAmountCoversWithinTolerance(t *testing.T) {
	cases := []struct {
		name      string
		amount    string
		required  string
		tolerance string
		want      bool
	}{
		{"exact with no tolerance", "17", "17.000000", "0", true},
		{"empty tolerance is exact", "17", "17", "", true},
		{"over by a unit with no tolerance", "17.000001", "17", "0", false},
		{"under by a unit with no tolerance", "16.999999", "17", "0", false},
		{"exact inside a tolerance", "17", "17", "0.01", true},
		{"over inside the tolerance", "17.005", "17", "0.01", true},
		{"at the tolerance", "17.01", "17", "0.01", true},
		{"beyond the tolerance", "17.02", "17", "0.01", false},
		// A short deposit never covers the order, whatever the tolerance.
		{"under inside the tolerance", "16.995", "17", "0.01", false},
		{"malformed amount", "abc", "17", "0.01", false},
		{"malformed tolerance", "17", "17", "-1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AmountCoversWithinTolerance(c.amount, c.required, c.tolerance, 6)
			if got != c.want {
				t.Fatalf("AmountCoversWithinTolerance(%q, %q, %q) = %v, want %v", c.amount, c.required, c.tolerance, got, c.want)
			}
		})
	}
}

func TestSumAmounts(t *testing.T) {
	got, err := SumAmounts(6, "17", "0.5", "1.000001")
	if err != nil || got != "18.500001" {
		t.Fatalf("SumAmounts = %q, %v; want 18.500001", got, err)
	}
	if got, err := SumAmounts(2); err != nil || got != "0.00" {
		t.Fatalf("SumAmounts() = %q, %v; want 0.00", got, err)
	}
	if _, err := SumAmounts(6, "17", "abc"); err == nil {
		t.Fatal("expected a malformed amount to be refused")
	}
}
