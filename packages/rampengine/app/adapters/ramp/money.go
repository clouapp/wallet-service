package ramp

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Money arithmetic for estimates and fee snapshots.
//
// Every operation goes through big.Rat and returns a decimal string, because
// these values are amounts a user is quoted and later charged. float64 is not
// an option: it cannot represent 0.1, and the error compounds across a
// multiply-then-subtract chain until an estimate and the order built from it
// disagree in the last place.
//
// Rounding is half-up except where a function documents otherwise —
// SourceForMinimum rounds up and SourceForMaximum rounds down, because a
// nearest-value answer to a bound question is wrong in one direction.
// Estimates here are non-binding, so the last place is presentation rather
// than settlement — what matters is that the same input always produces the
// same output.

// FeeAmount returns the fee charged on amount at feePercent, at scale decimal
// places. feePercent is the value stored in `rail_routing_settings` ("1.50"),
// i.e. a percent, not a fraction.
func FeeAmount(amount, feePercent string, scale int) (string, error) {
	base, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	percent, err := parseAmount(feePercent, "fee percent")
	if err != nil {
		return "", err
	}
	if percent.Cmp(oneHundred) > 0 {
		return "", fmt.Errorf("fee percent %q exceeds 100", feePercent)
	}
	if err := checkScale(scale); err != nil {
		return "", err
	}

	fee := new(big.Rat).Mul(base, percent)
	fee.Quo(fee, oneHundred)
	return fee.FloatString(scale), nil
}

// ConvertAmount returns amount multiplied by rate, at scale decimal places.
// The scale belongs to the currency being produced: two places for BRL, six
// for a stablecoin.
func ConvertAmount(amount, rate string, scale int) (string, error) {
	base, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	factor, err := parseAmount(rate, "rate")
	if err != nil {
		return "", err
	}
	if err := checkScale(scale); err != nil {
		return "", err
	}
	return new(big.Rat).Mul(base, factor).FloatString(scale), nil
}

// SubtractAmount returns amount minus deduction, at scale decimal places.
//
// A negative result is refused rather than returned. It means the rail's fee
// exceeds the order, which is a misconfiguration; letting it through would
// produce a transfer that pays out less than nothing, and the provider would
// be the one to notice.
func SubtractAmount(amount, deduction string, scale int) (string, error) {
	base, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	sub, err := parseAmount(deduction, "deduction")
	if err != nil {
		return "", err
	}
	if err := checkScale(scale); err != nil {
		return "", err
	}

	result := new(big.Rat).Sub(base, sub)
	if result.Sign() < 0 {
		return "", fmt.Errorf("deduction %q exceeds amount %q", deduction, amount)
	}
	return result.FloatString(scale), nil
}

// AmountCoversWithinTolerance reports whether amount covers required and
// exceeds it by at most tolerance (absolute), both read at scale decimal
// places. A short amount never covers, whatever the tolerance: paying out an
// order its deposit fell short of spends money the user never sent. tolerance
// "0" (or empty) requires an exact match. A malformed input never matches.
func AmountCoversWithinTolerance(amount, required, tolerance string, scale int) bool {
	if strings.TrimSpace(tolerance) == "" {
		tolerance = "0"
	}
	a, errA := scaledRat(amount, "amount", scale)
	r, errR := scaledRat(required, "required", scale)
	tol, errT := scaledRat(tolerance, "tolerance", scale)
	if errA != nil || errR != nil || errT != nil {
		return false
	}
	over := new(big.Rat).Sub(a, r)
	return over.Sign() >= 0 && over.Cmp(tol) <= 0
}

// scaledRat parses value and rounds it to scale, so a comparison sees the
// amounts a provider at that precision would.
func scaledRat(value, field string, scale int) (*big.Rat, error) {
	if err := checkScale(scale); err != nil {
		return nil, err
	}
	parsed, err := parseAmount(value, field)
	if err != nil {
		return nil, err
	}
	rounded, ok := new(big.Rat).SetString(parsed.FloatString(scale))
	if !ok {
		return nil, fmt.Errorf("%s %q does not round to scale %d", field, value, scale)
	}
	return rounded, nil
}

// decimalAmount is the only shape a money string may take here: digits, at
// most one dot, digits.
//
// It is an allow-list because big.Rat.SetString's grammar is far wider than a
// blocklist can track. Beyond the obvious "3/4" and "1e2" it accepts hex and
// binary floats with binary exponents ("0x1p10" and "1p10" are both 1024),
// underscore digit separators ("1_000"), and a leading sign ("+5"). Every one
// of those parses to a plausible number that nobody typed as an amount.
var decimalAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// parseAmount reads a non-negative decimal.
//
// The HTTP requests already reject non-amounts at the edge with anchored
// regexes of their own. This is the floor under everything that does not come
// through a form request — a provider's quoted rate, a stored setting, a
// hand-edited row.
func parseAmount(value, field string) (*big.Rat, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%s is empty", field)
	}
	if !decimalAmount.MatchString(trimmed) {
		return nil, fmt.Errorf("%s %q is not a decimal", field, value)
	}
	parsed, ok := new(big.Rat).SetString(trimmed)
	if !ok {
		return nil, fmt.Errorf("%s %q is not a number", field, value)
	}
	return parsed, nil
}

// ScaleAmount renders amount at exactly scale decimal places.
//
// It exists for the seam between a stored amount and a provider's accounting
// precision. A numeric(38,18) column comes back from Postgres with all
// eighteen decimals — "88.200000000000000000" — and a provider that documents
// six rejects that as malformed. The value is unchanged; only how many digits
// it is written with is.
//
// It rounds half-up, like every function here that is not answering a bound
// question, so it is safe on an amount carrying more precision than the target
// scale can hold. That case should not
// reach a money path — it would mean losing value — and the caller that hits it
// is asking the wrong question, not being silently corrected.
func ScaleAmount(amount string, scale int) (string, error) {
	if err := checkScale(scale); err != nil {
		return "", err
	}
	parsed, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	return parsed.FloatString(scale), nil
}

// FloorAmount truncates amount to scale decimal places.
//
// It is ScaleAmount's answer for the case ScaleAmount refuses to answer
// silently: an amount carrying more precision than a provider moves. The
// caller that uses it has to show the result as the amount that moves, rather
// than send it in place of the one the user asked for. It rounds down because
// the amount is funded from a balance, and rounding up would ask for money the
// user does not have.
func FloorAmount(amount string, scale int) (string, error) {
	if err := checkScale(scale); err != nil {
		return "", err
	}
	parsed, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	return floorRat(parsed, scale).FloatString(scale), nil
}

func checkScale(scale int) error {
	if scale < 0 {
		return fmt.Errorf("scale %d is negative", scale)
	}
	return nil
}

// QuotedAmounts is what a provider already calculated: Source produced
// Receive, and Receive was rounded at ReceiveScale.
//
// The scale travels with the pair because inverting the proportion has to know
// how much of Receive is the quote and how much is rounding. A caller that
// leaves it out inherits an error it cannot see, amplified by how far the
// target sits from Receive — the further, the larger.
type QuotedAmounts struct {
	Source       string
	Receive      string
	ReceiveScale int
}

// SourceForMinimum returns the smallest source amount that still reaches
// targetReceive on this quote.
//
// It exists for the rail minimum, which is configured in BRL while the user
// types in a token. Dividing the minimum by the quoted rate would ignore the
// fee that quote already deducted: the user would type the answer, the fee
// would eat the difference, and the amount would land under the minimum again
// — the loop this is meant to end. Scaling the whole quote keeps the fee in,
// because a percentage fee leaves receive linear in source.
//
// Receive arrives already rounded, so the quote may really have produced up to
// half a unit less than it shows. The proportion is taken against the low end
// of that interval and then rounded up, twice away from the bound: a suggested
// amount that is itself rejected is worse than no suggestion at all. Rounding
// up alone is not enough — the proportion multiplies the rounding error by
// target/receive, which on a small quote is larger than one unit of scale.
func SourceForMinimum(q QuotedAmounts, targetReceive string, scale int) (string, error) {
	from, produced, target, err := q.parse(targetReceive, scale)
	if err != nil {
		return "", err
	}

	produced.Sub(produced, halfUnit(q.ReceiveScale))
	if produced.Sign() <= 0 {
		return "", fmt.Errorf("receive %q does not exceed its own rounding interval", q.Receive)
	}

	return ceilString(proportion(from, target, produced), scale), nil
}

// SourceForMaximum returns the largest source amount that stays at or under
// targetReceive on this quote.
//
// It is the mirror of SourceForMinimum, one bound over: the rail's cap is in
// BRL and the form is in a token. Every direction is reversed — the high end
// of the rounding interval rather than the low end, and rounding down rather
// than up — because here the suggestion has to stay under a ceiling instead of
// clearing a floor. Reusing the minimum's rounding here would suggest an
// amount over the cap, which is the same loop seen from the other side.
func SourceForMaximum(q QuotedAmounts, targetReceive string, scale int) (string, error) {
	from, produced, target, err := q.parse(targetReceive, scale)
	if err != nil {
		return "", err
	}

	produced.Add(produced, halfUnit(q.ReceiveScale))

	// A cap that floors to nothing at this scale has no amount to suggest, and
	// telling the user to type zero is worse than naming only the cap. The
	// caller already drops the token figure when this fails.
	floored := floorRat(proportion(from, target, produced), scale)
	if floored.Sign() == 0 {
		return "", fmt.Errorf("target receive %q floors to zero at scale %d", targetReceive, scale)
	}

	return floored.FloatString(scale), nil
}

// parse validates the pair, the target and both scales once, so the two
// exported functions differ only in the direction they take from here.
func (q QuotedAmounts) parse(targetReceive string, scale int) (from, produced, target *big.Rat, err error) {
	if from, err = parseAmount(q.Source, "source"); err != nil {
		return nil, nil, nil, err
	}
	if produced, err = parseAmount(q.Receive, "receive"); err != nil {
		return nil, nil, nil, err
	}
	if target, err = parseAmount(targetReceive, "target receive"); err != nil {
		return nil, nil, nil, err
	}
	if produced.Sign() == 0 {
		return nil, nil, nil, fmt.Errorf("receive %q is zero", q.Receive)
	}
	if err = checkScale(q.ReceiveScale); err != nil {
		return nil, nil, nil, err
	}
	if err = checkScale(scale); err != nil {
		return nil, nil, nil, err
	}
	return from, produced, target, nil
}

// proportion is source * target / produced, in one chain: rounding the
// division first and multiplying second would round twice, and the second
// rounding lands on the number the user is told to type.
func proportion(from, target, produced *big.Rat) *big.Rat {
	result := new(big.Rat).Mul(from, target)
	return result.Quo(result, produced)
}

// halfUnit is half of the last place at scale — the most that rounding half-up
// at that scale can have moved a value.
func halfUnit(scale int) *big.Rat {
	return new(big.Rat).SetFrac(
		big.NewInt(5),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)+1), nil),
	)
}

// floorRat truncates value to scale decimal places. Truncation is the floor
// because parseAmount admits no negatives. It returns a rational rather than a
// string so the caller can test the result before rendering it.
func floorRat(value *big.Rat, scale int) *big.Rat {
	shift := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	shifted := new(big.Rat).Mul(value, new(big.Rat).SetInt(shift))
	return new(big.Rat).SetFrac(new(big.Int).Quo(shifted.Num(), shifted.Denom()), shift)
}

// ceilString renders a non-negative rational at scale decimal places, rounding
// away from zero rather than to nearest. parseAmount admits no negatives, so
// the sign case that would make "ceil" and "away from zero" differ cannot
// reach here.
func ceilString(value *big.Rat, scale int) string {
	shift := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	shifted := new(big.Rat).Mul(value, new(big.Rat).SetInt(shift))

	quotient, remainder := new(big.Int).QuoRem(shifted.Num(), shifted.Denom(), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	return new(big.Rat).SetFrac(quotient, shift).FloatString(scale)
}

// SumAmounts adds amounts at scale decimal places. It exists for a balance
// that several orders draw on: what the balance must cover is their total.
func SumAmounts(scale int, amounts ...string) (string, error) {
	if err := checkScale(scale); err != nil {
		return "", err
	}
	total := new(big.Rat)
	for _, amount := range amounts {
		parsed, err := parseAmount(amount, "amount")
		if err != nil {
			return "", err
		}
		total.Add(total, parsed)
	}
	return total.FloatString(scale), nil
}

// DivideAmount returns amount divided by divisor, at scale decimal places.
//
// It exists because a currency pair is quoted in one direction only. Bridge
// publishes usd→brl and refuses brl→usd, so an on-ramp priced in BRL has to
// divide by the quoted rate rather than multiply by an inverted copy of it —
// inverting first and multiplying second rounds twice, and the second rounding
// lands on the amount the user is quoted.
func DivideAmount(amount, divisor string, scale int) (string, error) {
	base, err := parseAmount(amount, "amount")
	if err != nil {
		return "", err
	}
	rate, err := parseAmount(divisor, "rate")
	if err != nil {
		return "", err
	}
	if rate.Sign() == 0 {
		return "", fmt.Errorf("rate %q is zero", divisor)
	}
	if err := checkScale(scale); err != nil {
		return "", err
	}
	return new(big.Rat).Quo(base, rate).FloatString(scale), nil
}

// SpreadCost is what converting amount at applied rather than at market costs
// the user: the share lost, as a percent at two places, and its value in reais
// at two places. multiply says the rates are destination-per-source; otherwise
// the amount divides by them. fiatIn says amount is the reais leg (an on-ramp);
// otherwise the reais are what the conversion produces (an off-ramp).
//
// A rate no worse than the market costs nothing, and both come back empty: a
// quote names a gap only when there is one.
func SpreadCost(amount, applied, market string, multiply, fiatIn bool) (percent, fiat string, err error) {
	base, err := parseAmount(amount, "amount")
	if err != nil {
		return "", "", err
	}
	appliedRate, err := parseAmount(applied, "applied rate")
	if err != nil {
		return "", "", err
	}
	marketRate, err := parseAmount(market, "market rate")
	if err != nil {
		return "", "", err
	}
	if appliedRate.Sign() == 0 || marketRate.Sign() == 0 {
		return "", "", fmt.Errorf("rate is zero (applied %q, market %q)", applied, market)
	}

	// Share of the output kept at the applied rate, against the market's.
	kept := new(big.Rat).Quo(appliedRate, marketRate)
	if !multiply {
		kept = new(big.Rat).Quo(marketRate, appliedRate)
	}
	lost := new(big.Rat).Sub(big.NewRat(1, 1), kept)
	if lost.Sign() <= 0 {
		return "", "", nil
	}

	value := new(big.Rat).Mul(base, lost)
	if !fiatIn {
		atMarket := new(big.Rat).Mul(base, marketRate)
		if !multiply {
			atMarket = new(big.Rat).Quo(base, marketRate)
		}
		value = new(big.Rat).Mul(atMarket, lost)
	}
	return new(big.Rat).Mul(lost, oneHundred).FloatString(2), value.FloatString(2), nil
}
