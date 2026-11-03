package bridge

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// issueCodes is an endorsement's requirements.issues: what Bridge found
// wrong. The list mixes bare codes ("tax_id_issuing_country_mismatch") with
// objects that key a list of codes by the field they concern
// ({"place_of_birth":["place_of_birth_missing"]}, the sandbox on 07/10/2026),
// so it is read for every code it carries, in the order sent, once each.
//
// It never fails: an unreadable issue list would fail the whole customer
// decode, and on a create that loses the customer Bridge has just made — the
// next submission then collides with it. Whatever names no code (a number, a
// nested object, a list that is not one) is skipped.
type issueCodes []string

func (c *issueCodes) UnmarshalJSON(data []byte) error {
	var items []json.RawMessage
	if json.Unmarshal(data, &items) != nil {
		*c = nil
		return nil
	}
	var out []string
	for _, item := range items {
		for _, code := range issueItemCodes(item) {
			if code != "" && !slices.Contains(out, code) {
				out = append(out, code)
			}
		}
	}
	*c = out
	return nil
}

// subjectKeys name whom a business customer's item is about rather than what
// is wrong: a partner keyed by id ({"associated_person": "ap_1", "items":
// [...]}) or, in missing's shape, by type and id. Their values are ids, never
// codes.
var subjectKeys = map[string]bool{"associated_person": true, "object_type": true, "object_id": true}

// issueItemCodes reads one item: a code, or an object whose values are each a
// code or a list of codes, walked in the order its keys were sent, its
// subject keys skipped.
func issueItemCodes(item json.RawMessage) []string {
	var code string
	if json.Unmarshal(item, &code) == nil {
		return []string{code}
	}
	dec := json.NewDecoder(bytes.NewReader(item))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return nil
	}
	var out []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return out
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return out
		}
		if name, _ := key.(string); subjectKeys[name] {
			continue
		}
		out = append(out, issueValueCodes(value)...)
	}
	return out
}

func issueValueCodes(value json.RawMessage) []string {
	var code string
	if json.Unmarshal(value, &code) == nil {
		return []string{code}
	}
	var list []json.RawMessage
	if json.Unmarshal(value, &list) != nil {
		return nil
	}
	var out []string
	for _, entry := range list {
		var listed string
		if json.Unmarshal(entry, &listed) == nil {
			out = append(out, listed)
		}
	}
	return out
}

// normalizedIssue is an issue code as the adapter compares it, trimmed and
// lower-cased: the rejection's categories and the region state read issues
// the same way, so a stray space or capital neither leaves a refusal unmapped
// nor hides a rail Bridge ruled out.
func normalizedIssue(issue string) string {
	return strings.ToLower(strings.TrimSpace(issue))
}
