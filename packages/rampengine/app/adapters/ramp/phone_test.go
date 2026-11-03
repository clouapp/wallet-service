package ramp

import (
	"strings"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		country string
		want    string
		wantErr bool
	}{
		// The shape the Android app actually sends: form.phone stripped to
		// digits, no calling code, because the UI shows "+55" as a static
		// prefix next to the field.
		{"brazilian mobile as bare digits", "11987654321", "BRA", "+5511987654321", false},
		{"brazilian landline as bare digits", "1133334444", "BRA", "+551133334444", false},
		{"alpha-2 country is accepted too", "11987654321", "BR", "+5511987654321", false},
		{"already E.164 passes through", "+5511987654321", "BRA", "+5511987654321", false},
		// A national number that already carries the trunk code must not be
		// prefixed twice into +5555…
		{"calling code without the plus", "5511987654321", "BRA", "+5511987654321", false},
		{"human separators are dropped", " (11) 98765-4321 ", "BRA", "+5511987654321", false},
		{"us number", "4155552671", "USA", "+14155552671", false},
		// An E.164 number for a country the table does not know still passes:
		// the caller already did the work, and the table is only needed to
		// supply a missing calling code.
		{"E.164 for an unlisted country", "+351912345678", "PRT", "+351912345678", false},
		{"portuguese mobile with spaces", "+351 912 345 678", "PRT", "+351912345678", false},
		{"E.164 parsed for any country regardless of declared one", "+447911123456", "BRA", "+447911123456", false},
		// Accepted before the library arrived; installed apps send them.
		{"BR mobile without the 9th digit", "+5511887654321", "BRA", "+5511887654321", false},
		{"BR mobile without 9th digit, separators", "+55 11 8765-4321", "BRA", "+551187654321", false},
		{"BR placeholder number", "+5511 0000 0000", "BRA", "+551100000000", false},
		{"US 555 number", "+15550100000", "USA", "+15550100000", false},
		{"empty phone stays empty", "", "BRA", "", false},
		{"only separators is empty", "  -  ", "BRA", "", false},

		{"bare digits for an unlisted country", "912345678", "PRT", "", true},
		{"too short to be a phone number", "1198", "BRA", "", true},
		{"letters are not a phone number", "phone-me", "BRA", "", true},
		{"longer than E.164 allows", "119876543210987654", "BRA", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizePhone(c.raw, c.country)
			if c.wantErr {
				if err == nil {
					t.Fatalf("NormalizePhone(%q, %q) = %q, want an error", c.raw, c.country, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePhone(%q, %q): %v", c.raw, c.country, err)
			}
			if got != c.want {
				t.Fatalf("NormalizePhone(%q, %q) = %q, want %q", c.raw, c.country, got, c.want)
			}
		})
	}
}

func TestSplitPhone(t *testing.T) {
	cases := []struct {
		name       string
		e164       string
		wantCode   string
		wantNumber string
		wantErr    bool
	}{
		{"brazil", "+5511987654321", "+55", "11987654321", false},
		{"united states", "+14155552671", "+1", "4155552671", false},
		// The bug this whole change exists for: a number with no calling code
		// must not become a payload with an empty country_code.
		{"no calling code at all", "11987654321", "", "", true},
		{"portugal", "+351912345678", "+351", "912345678", false},
		{"argentina", "+5491123456789", "+54", "91123456789", false},
		{"united kingdom", "+447911123456", "+44", "7911123456", false},
		{"brazil without 9th digit", "+5511887654321", "+55", "11887654321", false},
		{"us 555 number", "+15550100000", "+1", "5550100000", false},
		{"unknown calling code", "+99912345678", "", "", true},
		{"empty", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, number, err := SplitPhone(c.e164)
			if c.wantErr {
				if err == nil {
					t.Fatalf("SplitPhone(%q) = %q/%q, want an error", c.e164, code, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitPhone(%q): %v", c.e164, err)
			}
			if code != c.wantCode || number != c.wantNumber {
				t.Fatalf("SplitPhone(%q) = %q/%q, want %q/%q", c.e164, code, number, c.wantCode, c.wantNumber)
			}
		})
	}
}

// A phone number must never travel inside an error string. ozav/kyc_adapter.go
// wraps a SplitPhone failure, the service wraps that, and the controller hands
// it to responses.InternalError, which logs the error verbatim — so a wrap here
// puts the subscriber's number in the application log. The caller already holds
// the value it passed in; the log does not need it.
//
// Country stays: it is configuration, not the person.
func TestPhoneErrorsCarryNoNumber(t *testing.T) {
	const number = "987654321098"

	cases := []struct {
		name string
		call func() error
	}{
		{"normalize with no calling code available", func() error {
			_, err := NormalizePhone(number, "PRT")
			return err
		}},
		{"normalize a number that is not national", func() error {
			_, err := NormalizePhone(number, "BRA")
			return err
		}},
		{"normalize an E.164 number that is malformed", func() error {
			_, err := NormalizePhone("+0"+number+number, "BRA")
			return err
		}},
		{"split a number that is not E.164", func() error {
			_, _, err := SplitPhone(number)
			return err
		}},
		{"split an unknown calling code", func() error {
			_, _, err := SplitPhone("+999" + number)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), number) {
				t.Fatalf("error names the phone number: %v", err)
			}
		})
	}
}
