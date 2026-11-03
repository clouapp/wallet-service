package ramp

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// Phone numbers travel through the contract as a single E.164 string
// (`+5511987654321`). That is a promise this file exists to keep: every
// adapter downstream may assume it, and no adapter has to guess at it.
//
// It was not always so. Profile.Phone carried whatever the client typed, and
// each adapter invented its own reading: Bridge forwarded the raw string,
// Ozav split on a hardcoded `+55` prefix and, when it did not match, sent a
// customer with an empty `individual.phone.country_code` — which Ozav
// rejects, in a message naming a field the client never sent. The Android app
// posts `form.phone` stripped to digits (the "+55" beside the field is UI, not
// data), so that path was every Brazilian user, not an edge case.

// callingCode is what the contract needs to know about a country to put a
// national number into E.164: the code itself, and how long a national number
// is there. The length range is not decoration — it is what tells a number
// that already carries the calling code from one that merely begins with the
// same digits. `55` is a valid Brazilian area code, so `5511987654321` is
// ambiguous on its digits alone and unambiguous on its length.
type callingCode struct {
	code   string
	minLen int
	maxLen int
}

// callingCodes is the compatibility rule for numbers WITHOUT a `+`: only
// BRA/USA may arrive as bare national digits (old app builds do), keyed by
// both alpha-3 and alpha-2. Any other country fails loudly rather than being
// prefixed with a guess. Numbers written with a `+` are parsed by
// phonenumbers for every country and need no entry here.
var callingCodes = map[string]callingCode{
	// Brazilian national numbers are area code (2) + subscriber (8 landline,
	// 9 mobile).
	"BRA": {code: "+55", minLen: 10, maxLen: 11},
	"BR":  {code: "+55", minLen: 10, maxLen: 11},
	// NANP: area code (3) + subscriber (7).
	"USA": {code: "+1", minLen: 10, maxLen: 10},
	"US":  {code: "+1", minLen: 10, maxLen: 10},
}

// e164Pattern is the ITU-T E.164 shape: a leading `+`, a non-zero country
// code digit, and 8 to 15 digits in total.
var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)

// phoneSeparators are the characters a human types into a phone field that
// carry no information. Anything outside this set and the digits is a typo,
// not a separator, and NormalizePhone refuses it rather than silently
// deleting part of what the user entered.
const phoneSeparators = " -(). \t/"

// NormalizePhone converts a phone number as typed into the E.164 form the
// contract promises, using country (alpha-2 or alpha-3) to supply a calling
// code the number does not carry.
//
// An empty phone normalizes to empty rather than to an error: the field is
// optional at the API boundary, and an application that has not collected a
// phone must not be refused over one.
func NormalizePhone(raw, country string) (string, error) {
	digits, hasPlus, err := phoneDigits(raw)
	if err != nil {
		return "", err
	}
	if digits == "" {
		return "", nil
	}

	if hasPlus {
		e164 := "+" + digits
		if !e164Pattern.MatchString(e164) {
			return "", errors.New("phone is not a valid E.164 number")
		}
		return e164, nil
	}

	cc, ok := callingCodes[normalizeCountryKey(country)]
	if !ok {
		return "", fmt.Errorf("phone has no calling code and country %q supplies none: send it in E.164 form", country)
	}

	switch {
	case len(digits) >= cc.minLen && len(digits) <= cc.maxLen:
		return cc.code + digits, nil
	case strings.HasPrefix(digits, strings.TrimPrefix(cc.code, "+")):
		national := strings.TrimPrefix(digits, strings.TrimPrefix(cc.code, "+"))
		if len(national) >= cc.minLen && len(national) <= cc.maxLen {
			return cc.code + national, nil
		}
	}
	return "", fmt.Errorf("phone is not a valid national number for %q", country)
}

// SplitPhone breaks an E.164 number into the calling code and national number
// that providers modelling the two separately require.
//
// It errors rather than returning an empty calling code. A provider that
// wants the pair wants both halves; handing it one and blanking the other
// produces a rejection upstream that names a field the caller never sent.
func SplitPhone(e164 string) (string, string, error) {
	e164 = strings.TrimSpace(e164)
	if !e164Pattern.MatchString(e164) {
		return "", "", errors.New("phone is not in E.164 form")
	}
	// +55 and +1 keep the plain prefix split: it accepts every number the
	// validation accepts, including ones the numbering plan would call
	// invalid (installed apps send them).
	for _, code := range []string{"+55", "+1"} {
		if strings.HasPrefix(e164, code) {
			return code, strings.TrimPrefix(e164, code), nil
		}
	}
	// Any other calling code: the library only reads the split. Validity is
	// deliberately not checked, matching NormalizePhone's acceptance.
	num, err := phonenumbers.Parse(e164, "")
	if err != nil {
		return "", "", errors.New("phone carries a calling code this contract does not know")
	}
	return "+" + strconv.Itoa(int(num.GetCountryCode())), phonenumbers.GetNationalSignificantNumber(num), nil
}

// phoneDigits strips the separators a human types and reports whether the
// number was written with a leading `+`.
func phoneDigits(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	hasPlus := strings.HasPrefix(raw, "+")
	raw = strings.TrimPrefix(raw, "+")

	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(phoneSeparators, r):
		default:
			return "", false, fmt.Errorf("phone contains %q, which is neither a digit nor a separator", r)
		}
	}
	return b.String(), hasPlus, nil
}

// normalizeCountryKey accepts the alpha-2 and alpha-3 spellings, in any case,
// that the KYC request allows.
func normalizeCountryKey(country string) string {
	return strings.ToUpper(strings.TrimSpace(country))
}
