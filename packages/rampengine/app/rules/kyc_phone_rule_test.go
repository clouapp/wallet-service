package rules

import (
	"context"
	"testing"
)

func TestKycPhoneRule_Passes(t *testing.T) {
	r := &KycPhoneRule{}
	ctx := context.Background()

	t.Run("brazil", func(t *testing.T) {
		data := mapData{"country": "BRA"}
		// The Android app posts the phone stripped to digits; the fan-out
		// smoke posts the same shape. Both must pass here, because the
		// controller canonicalises them into E.164 afterwards.
		valid := []string{"11987654321", "1133334444", "(11) 98765-4321", "+5511987654321", "5511987654321", ""}
		for _, v := range valid {
			if !r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be valid for BRA", v)
			}
		}
		// These are the ones that would otherwise reach a provider as a
		// customer with a blank country_code and come back as a rejection
		// naming a field the client never sent.
		invalid := []string{"1198", "phone-me", "119876543210987654"}
		for _, v := range invalid {
			if r.Passes(ctx, data, v) {
				t.Errorf("expected %q to be rejected for BRA", v)
			}
		}
	})

	t.Run("a country with no calling code in the table still takes E.164", func(t *testing.T) {
		data := mapData{"country": "PRT"}
		if !r.Passes(ctx, data, "+351912345678") {
			t.Error("expected an E.164 number to be valid for PRT")
		}
		if r.Passes(ctx, data, "912345678") {
			t.Error("expected a bare national number to be rejected for PRT")
		}
	})
}
