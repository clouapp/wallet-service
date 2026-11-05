package rules

import (
	"context"
	"strings"
	"testing"
)

// The canonical spelling is what both apps send today when they save a key
// (PixKeyValidator.normalized on Android and iOS) and what their display
// formatters read back: a CPF of 11 digits, a CNPJ of 14, a phone as +55 with
// DDD and number. Anything else read back crashed the key list on both apps.
func TestPixKeyCanonicalRule_Passes(t *testing.T) {
	r := &PixKeyCanonicalRule{}
	cases := map[string]struct {
		valid, invalid []string
	}{
		"cpf": {
			valid:   []string{"52998224725", "39053344705", "11144477735", "12345678909"},
			invalid: []string{"52998224724", "11111111111", "3905334470", "390533447051", "390.533.447-05", " 39053344705", "3905334470a"},
		},
		"cnpj": {
			valid:   []string{"12345678000195", "11222333000181"},
			invalid: []string{"12345678000196", "00000000000000", "1234567800019", "12.345.678/0001-95"},
		},
		"phone": {
			// Mobile (9 + eight digits) and landline (eight digits) after the DDD.
			valid: []string{"+5511999999999", "+551133334444", "+5521987654321"},
			invalid: []string{
				"11999999999", "5511999999999", "+55 11 99999-9999", "+55119999999",
				"+5501999999999", "+5511899999999", "+14155552671", "+551199999999999",
			},
		},
		"email": {
			valid:   []string{"maria@example.com", "Maria@Example.com", "a.b+c@sub.example.com.br"},
			invalid: []string{"maria@example", "maria example.com", "@example.com", "maria@@example.com", " maria@example.com", strings.Repeat("a", 66) + "@example.com"},
		},
		"evp": {
			valid:   []string{"550e8400-e29b-41d4-a716-446655440000", "550E8400-E29B-41D4-A716-446655440000"},
			invalid: []string{"550e8400e29b41d4a716446655440000", "550e8400-e29b-41d4-a716-44665544000", "not-a-uuid", "550e8400-e29b-41d4-a716-44665544000g"},
		},
	}
	for keyType, c := range cases {
		t.Run(keyType, func(t *testing.T) {
			data := mapData{"pix_key_type": keyType}
			for _, v := range c.valid {
				if !r.Passes(context.Background(), data, v) {
					t.Errorf("expected %s %q to be accepted", keyType, v)
				}
			}
			for _, v := range c.invalid {
				if r.Passes(context.Background(), data, v) {
					t.Errorf("expected %s %q to be rejected", keyType, v)
				}
			}
		})
	}
}

// An email key is at most 77 characters in the DICT, and both apps refuse
// longer ones.
func TestPixKeyCanonicalRule_EmailLengthCap(t *testing.T) {
	r := &PixKeyCanonicalRule{}
	data := mapData{"pix_key_type": "email"}
	local := strings.Repeat("a", 77-len("@example.com"))

	if !r.Passes(context.Background(), data, local+"@example.com") {
		t.Error("expected a 77-character email to be accepted")
	}
	if r.Passes(context.Background(), data, local+"a@example.com") {
		t.Error("expected a 78-character email to be rejected")
	}
}

// A type outside the BACEN set is pix_key_type's own error; this rule does not
// add a second one about a key it cannot judge.
func TestPixKeyRules_LeaveAnUnknownTypeToItsOwnRule(t *testing.T) {
	data := mapData{"pix_key_type": "boleto"}
	if !(&PixKeyCanonicalRule{}).Passes(context.Background(), data, "anything") {
		t.Error("canonical: expected an unknown type to be left to pix_key_type")
	}
	if !(&PixKeyTypedRule{}).Passes(context.Background(), data, "anything") {
		t.Error("typed: expected an unknown type to be left to pix_key_type")
	}
}

func TestPixKeyRules_RejectANonString(t *testing.T) {
	data := mapData{"pix_key_type": "cpf"}
	if (&PixKeyCanonicalRule{}).Passes(context.Background(), data, 39053344705) {
		t.Error("canonical: expected a number to be rejected")
	}
	if (&PixKeyTypedRule{}).Passes(context.Background(), data, 39053344705) {
		t.Error("typed: expected a number to be rejected")
	}
}

// The off-ramp takes a key however it was spelled (issue #73: production saw
// one key with and without +55, punctuated or not, and the backend finds the
// destination by the normalized key), but not a key that is no key at all once
// normalized.
func TestPixKeyTypedRule_Passes(t *testing.T) {
	r := &PixKeyTypedRule{}
	cases := map[string]struct {
		valid, invalid []string
	}{
		"cpf": {
			valid:   []string{"39053344705", "390.533.447-05", "390 533 447 05"},
			invalid: []string{"390.533.447-06", "3905334470", "abc39053344705", "390.533.447-05x"},
		},
		"cnpj": {
			valid:   []string{"12345678000195", "12.345.678/0001-95"},
			invalid: []string{"12.345.678/0001-96", "12.345.678/0001"},
		},
		"phone": {
			valid:   []string{"+5511999998888", "+55 11 99999-8888", "(11) 99999-8888", "11999998888", "5511999998888", "(11) 3333-4444"},
			invalid: []string{"+1 415 555 2671", "+11 99999-8888", "1199", "(11) 89999-8888", "call me", "11999998888x"},
		},
		"email": {
			valid:   []string{"maria@example.com", "  Maria@Example.COM  "},
			invalid: []string{"maria@example", "maria"},
		},
		"evp": {
			valid:   []string{"550e8400-e29b-41d4-a716-446655440000", " 550E8400-E29B-41D4-A716-446655440000 "},
			invalid: []string{"550e8400e29b41d4a716446655440000", "chave-aleatoria"},
		},
	}
	for keyType, c := range cases {
		t.Run(keyType, func(t *testing.T) {
			data := mapData{"pix_key_type": keyType}
			for _, v := range c.valid {
				if !r.Passes(context.Background(), data, v) {
					t.Errorf("expected %s %q to be accepted", keyType, v)
				}
			}
			for _, v := range c.invalid {
				if r.Passes(context.Background(), data, v) {
					t.Errorf("expected %s %q to be rejected", keyType, v)
				}
			}
		})
	}
}

// Whatever a saved key may be, the off-ramp must take: a key the list offers
// and the withdrawal refuses is useless.
func TestPixKeyTypedRule_AcceptsEveryCanonicalKey(t *testing.T) {
	keys := map[string]string{
		"cpf": "52998224725", "cnpj": "11222333000181", "phone": "+551133334444",
		"email": "maria@example.com", "evp": "550e8400-e29b-41d4-a716-446655440000",
	}
	for keyType, key := range keys {
		data := mapData{"pix_key_type": keyType}
		if !(&PixKeyCanonicalRule{}).Passes(context.Background(), data, key) {
			t.Fatalf("fixture %s %q is not canonical", keyType, key)
		}
		if !(&PixKeyTypedRule{}).Passes(context.Background(), data, key) {
			t.Errorf("expected canonical %s %q to be accepted by the off-ramp", keyType, key)
		}
	}
}

func TestIsValidCNPJ(t *testing.T) {
	cases := map[string]bool{
		"11222333000181": true,
		"12345678000195": true,
		"11222333000182": false,
		"11222333000191": false,
		"11111111111111": false,
		"1122233300018":  false,
	}
	for cnpj, want := range cases {
		if got := isValidCNPJ(cnpj); got != want {
			t.Errorf("isValidCNPJ(%q) = %v, want %v", cnpj, got, want)
		}
	}
}

// A company opened since 31/07/2026 may hold an alphanumeric CNPJ, and its
// CNPJ key is the CNPJ itself (spec §16). Saved, it is the canonical spelling:
// upper case, no punctuation. Withdrawn to, it is taken however it was typed,
// the way a numeric one is.
func TestPixKeyRules_AlphanumericCNPJ(t *testing.T) {
	data := mapData{"pix_key_type": "cnpj"}
	canonical, typed := &PixKeyCanonicalRule{}, &PixKeyTypedRule{}

	for _, key := range []string{"12ABC34501DE35", "12ABN34501DE35"} {
		if !canonical.Passes(context.Background(), data, key) {
			t.Errorf("canonical: expected %q to be accepted", key)
		}
	}
	for _, key := range []string{"12abc34501de35", "12ABC34501DE36", "12.ABC.345/01DE-35"} {
		if canonical.Passes(context.Background(), data, key) {
			t.Errorf("canonical: expected %q to be rejected", key)
		}
	}
	for _, key := range []string{"12ABC34501DE35", "12.ABC.345/01DE-35", "12.abc.345/01de-35", " 12 ABC 345 01DE 35 "} {
		if !typed.Passes(context.Background(), data, key) {
			t.Errorf("typed: expected %q to be accepted", key)
		}
	}
	for _, key := range []string{"12.ABC.345/01DE-36", "12.ABC.345/01DE", "12_ABC_345_01DE_35"} {
		if typed.Passes(context.Background(), data, key) {
			t.Errorf("typed: expected %q to be rejected", key)
		}
	}
	// A CPF stays digits: a letter is no spelling of one.
	if typed.Passes(context.Background(), mapData{"pix_key_type": "cpf"}, "390.533.447-0A") {
		t.Error("typed: expected a CPF with a letter to be rejected")
	}
}
