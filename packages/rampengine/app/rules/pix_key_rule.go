package rules

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
)

var (
	pixCPFPattern = regexp.MustCompile(`^\d{11}$`)
	// Twelve digits or upper-case letters, then two check digits: the
	// canonical spelling of a CNPJ, numeric or alphanumeric (spec §19.1).
	pixCNPJPattern = regexp.MustCompile(`^[0-9A-Z]{12}\d{2}$`)
	// +55, a DDD that does not start with 0, then a mobile (9 and eight
	// digits) or a landline (eight digits) number — the shape both apps'
	// PixKeyValidator normalize a phone to.
	pixPhonePattern = regexp.MustCompile(`^\+55[1-9]\d(9\d{8}|\d{8})$`)
	// The apps' own email check; the DICT, not this, decides whether it exists.
	pixEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	pixEVPPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// A CPF, CNPJ or phone as people write one: digits and the dots, dashes,
	// slashes, parentheses, spaces and plus sign of its mask, nothing else.
	pixTypedDigitsPattern = regexp.MustCompile(`^[\d\s.\-/()+]+$`)
)

// pixEmailMaxLen is the DICT's cap on an email key, which both apps enforce.
const pixEmailMaxLen = 77

// PixKeyCanonicalRule accepts a Pix key only in the one spelling the DICT
// stores, judged against the request's pix_key_type: a CPF as its 11 digits
// and a CNPJ as its 14 characters (digits, or upper-case letters in the first
// twelve), both with valid check digits; a phone as +55 with DDD
// and number; an email; a random key (evp) as a UUID. Email and evp are taken
// in any case, which is no malformation and which every reader lowercases.
//
// It guards the saved keys (POST/PUT /pix-keys). What is saved is what both
// apps read back and format for the list, and their formatters assert the
// length of a CPF or CNPJ: one malformed row crashed the whole list. Both apps
// already send exactly this spelling; the rule refuses what they would not.
type PixKeyCanonicalRule struct{}

func (r *PixKeyCanonicalRule) Signature() string { return "pix_key_canonical" }

func (r *PixKeyCanonicalRule) Passes(_ context.Context, data validation.Data, val any, _ ...any) bool {
	key, ok := val.(string)
	if !ok {
		return false
	}
	keyType := pixKeyTypeFromData(data)
	if !isPixKeyType(keyType) {
		return true
	}
	return validCanonicalPixKey(keyType, key)
}

func (r *PixKeyCanonicalRule) Message(_ context.Context) string {
	return "must be a valid key of its pix_key_type: 11 CPF digits, 14 CNPJ digits, a +55 phone, an email or a UUID"
}

// PixKeyTypedRule accepts a Pix key however it was typed, as long as it is a
// valid key of the request's pix_key_type once normalized the way the backend
// already normalizes it (brazil.NormalizePixKey): "390.533.447-05" and
// "(11) 99999-8888" pass, a CPF with a wrong check digit or a number that is
// no Brazilian phone does not.
//
// It guards the off-ramp (POST /offramps/estimate, POST /offramps). Production
// saw one key reach it with and without +55, punctuated or not (issue #73, which
// made the destination lookup normalize the key), and the iOS build on master
// withdraws to a saved key exactly as it was stored — so refusing a spelling
// there would break withdrawals that work. Every key the canonical rule
// accepts, this one accepts too: a saved key is always one the withdrawal
// takes.
type PixKeyTypedRule struct{}

func (r *PixKeyTypedRule) Signature() string { return "pix_key_typed" }

func (r *PixKeyTypedRule) Passes(_ context.Context, data validation.Data, val any, _ ...any) bool {
	key, ok := val.(string)
	if !ok {
		return false
	}
	keyType := pixKeyTypeFromData(data)
	if !isPixKeyType(keyType) {
		return true
	}
	switch keyType {
	case "cpf", "phone":
		if !pixTypedDigitsPattern.MatchString(key) {
			return false
		}
	case "cnpj":
		if !cnpjTypedPattern.MatchString(key) {
			return false
		}
	}
	// NormalizePixKey reads any number as Brazilian; one with another
	// country's code is not a Brazilian key, as both apps say.
	if trimmed := strings.TrimSpace(key); keyType == "phone" && strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+55") {
		return false
	}
	return validCanonicalPixKey(keyType, brazil.NormalizePixKey(keyType, key))
}

func (r *PixKeyTypedRule) Message(_ context.Context) string {
	return "must be a valid key of its pix_key_type"
}

func validCanonicalPixKey(keyType, key string) bool {
	switch keyType {
	case "cpf":
		return pixCPFPattern.MatchString(key) && isValidCPF(key)
	case "cnpj":
		return pixCNPJPattern.MatchString(key) && isValidCNPJ(key)
	case "phone":
		return pixPhonePattern.MatchString(key)
	case "email":
		return utf8.RuneCountInString(key) <= pixEmailMaxLen && pixEmailPattern.MatchString(key)
	case "evp":
		return pixEVPPattern.MatchString(key)
	default:
		return false
	}
}

// isPixKeyType is the BACEN set pixrules.KeyTypeRule enforces. A type outside
// it is that rule's error to report; the key rules pass rather than add a
// second error about a key they cannot judge.
func isPixKeyType(keyType string) bool {
	switch keyType {
	case "cpf", "cnpj", "phone", "email", "evp":
		return true
	default:
		return false
	}
}

func pixKeyTypeFromData(data validation.Data) string {
	if data == nil {
		return ""
	}
	raw, _ := data.Get("pix_key_type")
	keyType, _ := raw.(string)
	return keyType
}
