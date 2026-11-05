package requestlog

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const redacted = "[redacted]"

var jwtLike = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

var sensitiveHeaderKeys = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"x-webhook-signature": {},
	"x-ozav-signature":    {},
}

// sensitiveKeys names the body fields (at any depth), query parameters and
// route parameters whose value is never stored. It applies to every endpoint
// at once, so a name lands here only when everything that uses it carries
// personal data or a credential; a generic one (name, label, value) would
// blank what support reads to follow a request. email and handle stay
// readable on purpose: they are how support finds the account.
var sensitiveKeys = map[string]struct{}{
	"password":              {},
	"password_confirmation": {},
	"current_password":      {},
	"new_password":          {},
	"api_key":               {},
	"webhook_public_key":    {},
	"webhook_secret":        {},
	"secret":                {},
	"token":                 {},
	"refresh_token":         {},
	"code":                  {},
	"login_token":           {},
	"challenge_token":       {},
	"otpauth_uri":           {},
	"backup_codes":          {},
	// The hosted terms and verification pages, under every name they are
	// served as. Each is a per-customer capability link — Bridge's carries a
	// session_token that accepts the terms as that customer — so the page is a
	// credential and String's bearer/JWT sniff never sees it inside a URL.
	// `url` reads generic but is not: every one on this HTTP surface is such a
	// page. The single collateral is the `url` of a Bridge virtual-account
	// receipt in a webhook body, which support has no other use for.
	"tos_link_url":   {},
	"tos_link":       {},
	"kyc_link":       {},
	"hosted_kyc_url": {},
	"session_url":    {},
	"url":            {},
	// Settings values: the SES access key id and the SMTP username. `key` also
	// names each item of a settings response, which the `label` beside it
	// still identifies.
	"key":      {},
	"username": {},
	// The push credentials as an admin writes them: the APNs .p8 key of each
	// environment and the FCM service account file, which carries a private
	// key of its own.
	"private_key":            {},
	"sandbox_private_key":    {},
	"production_private_key": {},
	"service_account":        {},

	"pix_key":            {},
	"recipient_name":     {},
	"recipient_document": {},
	// Who paid a deposit, as the provider's webhook and the admin
	// transaction resource carry it.
	"sender_name":        {},
	"originator_name":    {},
	"originator_address": {},
	// A US bank account: the one a user saves for USD payouts (and its copy
	// at Bridge), the user's own USD virtual account as its instructions
	// spell it, and the routing of whoever paid an ACH deposit. bank_name is
	// redacted too, as the spec lists it: beside a payer's name or a last4 it
	// narrows down whose account it is.
	"bank_name":                  {},
	"account_number":             {},
	"routing_number":             {},
	"account_owner_name":         {},
	"bank_account_number":        {},
	"bank_routing_number":        {},
	"bank_beneficiary_name":      {},
	"bank_beneficiary_address":   {},
	"sender_bank_routing_number": {},

	"first_name":  {},
	"last_name":   {},
	"mother_name": {},
	"legal_name":  {},
	"full_name":   {},
	"phone":       {},
	"birth_date":  {},
	// date_of_birth is the same field in Ozav's spelling, which reaches a log
	// through the customer webhook rather than through a client request.
	"date_of_birth": {},
	"tax_id":        {},
	// With the rest of the profile, the admin reads it decrypted
	// (GET /admin/api/users/{id}/kyc-profile) and response bodies are logged.
	"nationality": {},
	// The country a foreign tax id was declared under names the person's
	// origin beside it (the admin KYC profile, the lookup body).
	"tax_id_country": {},
	// tax_id_type passes the same test: every use of the name is the identity
	// a person declared (the client application and ToS bodies, the admin
	// profile, edit and lookup), and "passport" says they hold no CPF — the
	// same origin, by another name. The catalog's own vocabulary is spelled
	// `type`, which stays readable.
	"tax_id_type": {},
	// Every issuing_country is the country that issued one person's document
	// (the client documents body, Bridge's identifying_information, Ozav's
	// uploads and travel-rule parties); for a passport that is their origin.
	"issuing_country": {},

	// residential_address is the container both providers nest an address in,
	// redacted whole so each one's leaf names (line1, street_line_1) need not
	// be enumerated. `state` is deliberately absent: Bridge spells a transfer's
	// status that way, and blanking it would hide what support reads.
	"residential_address": {},
	"street":              {},
	"street_line_1":       {},
	"street_line_2":       {},
	"number":              {},
	"neighborhood":        {},
	"city":                {},
	"subdivision":         {},
	"postal_code":         {},
	"cep":                 {},

	// A saved US bank account (bank beneficiaries, spec §5.4): the numbers, the
	// holder and the address lines the residential keys above do not cover.
	// city and postal_code are already there; the state is spelled state_code
	// because `state` stays readable (a transfer's status).
	"beneficiary_name": {},
	"address_line_1":   {},
	"address_line_2":   {},
	"state_code":       {},

	"occupation":      {},
	"source_of_funds": {},
	"account_purpose": {},
	// Bridge's free text beside an account_purpose of "other".
	"account_purpose_other": {},
	// The last four digits of a US resident's SSN or ITIN, which the client
	// profile carries in place of the number.
	"tax_id_last4":                 {},
	"monthly_income_band":          {},
	"monthly_income_usd":           {},
	"expected_monthly_volume_band": {},
	"expected_monthly_volume_usd":  {},
	"is_pep":                       {},

	"image_front":      {},
	"image_back":       {},
	"selfie":           {},
	"proof_of_address": {},
	"bank_statement":   {},

	// The KYB (spec §7, API contract §6.1). A company's CNPJ names a person
	// for an MEI or an EI, whose legal name is the owner's own; the trade
	// name and both addresses follow, and every associated person and
	// document is a person's data or a file, redacted whole.
	"cnpj":                      {},
	"trade_name":                {},
	"registered_address":        {},
	"physical_address":          {},
	"associated_persons":        {},
	"company_documents":         {},
	"person_identity_documents": {},
	// What the customers routes call a company where others may see it: an
	// MEI's is its owner's name (API contract §2.1).
	"display_name": {},
}

func sensitive(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(key)]
	return ok
}

// Redactor scrubs secrets before request logs are persisted.
type Redactor struct{}

func (r Redactor) Headers(h http.Header) map[string][]string {
	if h == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(h))
	for k, vals := range h {
		key := strings.ToLower(k)
		if _, ok := sensitiveHeaderKeys[key]; ok {
			out[k] = []string{redacted}
			continue
		}
		copied := make([]string, len(vals))
		for i, v := range vals {
			copied[i] = r.String(v)
		}
		out[k] = copied
	}
	return out
}

// Query redacts query-string values by the same names as a body.
func (r Redactor) Query(query map[string]string) map[string]string {
	out := make(map[string]string, len(query))
	for k, v := range query {
		if sensitive(k) {
			out[k] = redacted
			continue
		}
		out[k] = r.String(v)
	}
	return out
}

// Path redacts each segment bound to a sensitive route parameter, found by
// lining path up with the route's pattern: /api/postal-codes/{cep} stores
// /api/postal-codes/[redacted]. A request no route matched has no pattern and
// is kept as it arrived.
func (r Redactor) Path(path, pattern string) string {
	segments := strings.Split(path, "/")
	params := strings.Split(pattern, "/")
	if len(segments) != len(params) {
		return path
	}
	for i, param := range params {
		name, isParam := strings.CutPrefix(param, "{")
		if isParam && sensitive(strings.TrimSuffix(name, "}")) {
			segments[i] = redacted
		}
	}
	return strings.Join(segments, "/")
}

func (r Redactor) Body(contentType string, raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "multipart/form-data"):
		s := "[multipart omitted]"
		return &s
	case strings.Contains(ct, "application/x-www-form-urlencoded"):
		return r.formBody(raw)
	case strings.Contains(ct, "application/json"), ct == "" || strings.Contains(ct, "text/"):
		return r.textBody(string(raw))
	default:
		s := "[binary omitted]"
		return &s
	}
}

func (r Redactor) formBody(raw []byte) *string {
	vals, err := url.ParseQuery(string(raw))
	if err != nil {
		s := redacted
		return &s
	}
	out := make(map[string][]string, len(vals))
	for k, v := range vals {
		if sensitive(k) {
			out[k] = []string{redacted}
			continue
		}
		scrubbed := make([]string, len(v))
		for i, item := range v {
			scrubbed[i] = r.String(item)
		}
		out[k] = scrubbed
	}
	b, err := json.Marshal(out)
	if err != nil {
		s := redacted
		return &s
	}
	s := string(b)
	return &s
}

func (r Redactor) textBody(raw string) *string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	// A body that does not parse cannot be scrubbed field by field.
	s := redacted
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		if b, err := json.Marshal(r.scrubValue(v)); err == nil {
			s = string(b)
		}
	}
	return &s
}

func (r Redactor) scrubValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if sensitive(k) {
				out[k] = redacted
				continue
			}
			out[k] = r.scrubValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.scrubValue(val)
		}
		return out
	case string:
		return r.String(t)
	default:
		return v
	}
}

func (r Redactor) String(s string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "bearer ") {
		return redacted
	}
	if jwtLike.MatchString(s) {
		return redacted
	}
	return s
}

func TruncateBody(s *string, max int) *string {
	if s == nil || max <= 0 {
		return s
	}
	if len(*s) <= max {
		return s
	}
	truncated := (*s)[:max] + "[truncated]"
	return &truncated
}
