package ozav

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// rejectionMapping is a refusal translated onto the XIP categories, with
// Ozav's own codes kept for the admin.
type rejectionMapping struct {
	categories []string
	details    []string
	// unmapped names the codes nobody has mapped, for the warning.
	unmapped []string
}

// rejectionReasonCodes is Ozav's closed rejection vocabulary
// (DecisionReasonCode, "Rejection codes"). Every one of them is not_eligible:
// a rejection at Ozav is final — "no address reverses a verdict", in its own
// guides — so even identity_failed or document_invalid, which would reopen the
// wizard at Bridge, would only send the user to redo something Ozav will not
// look at again. Listing them is what separates a known final answer from a
// code nobody has read, which is logged.
var rejectionReasonCodes = map[string]bool{
	"sanctions_match":         true,
	"pep_unmitigated":         true,
	"identity_failed":         true,
	"document_invalid":        true,
	"capacity_unproven":       true,
	"inconsistent_data":       true,
	"restricted_jurisdiction": true,
	"no_response":             true,
	"fraud_suspected":         true,
	"policy":                  true,
}

// rejection translates a rejected customer. A reason code outside the
// vocabulary, or a rejection recorded before Ozav had one, is still
// not_eligible, and is reported as unmapped.
func (c customerResponse) rejection() rejectionMapping {
	if c.Status != "rejected" {
		return rejectionMapping{}
	}
	out := rejectionMapping{categories: []string{models.KycRejectionNotEligible}}
	if c.Decision == nil || c.Decision.ReasonCode == "" {
		out.unmapped = []string{"no reason code"}
		return out
	}
	out.details = []string{c.Decision.ReasonCode}
	if !rejectionReasonCodes[c.Decision.ReasonCode] {
		out.unmapped = []string{c.Decision.ReasonCode}
	}
	return out
}

// loggedRejection is rejection, warning about whatever it could not map. A
// customer that is not rejected carries instead why a file was asked for
// again, so the app can say what was wrong with the one sent.
func (c customerResponse) loggedRejection() rejectionMapping {
	mapping := c.rejection()
	if c.Status != "rejected" {
		mapping = c.documentReasons()
	}
	ramp.LogUnmappedRejection(string(ramp.ProviderOzav), mapping.unmapped)
	return mapping
}

// documentCategories is the category of each document type whose file can come
// back as invalid, in the vocabulary the app has words for. A document type
// outside it (a tax return asked for a limit increase) is not a reason the
// user is shown.
var documentCategories = map[string]string{
	"proof_of_address":  models.KycRejectionProofOfAddressInvalid,
	"bank_statement":    models.KycRejectionBankStatementInvalid,
	"identity_document": models.KycRejectionDocumentUnreadable,
}

// documentReasons maps the files still owed because the last one was invalid.
// Ozav's own words stay in details for the admin; the categories come out in
// the order the app lists them.
func (c customerResponse) documentReasons() rejectionMapping {
	var out rejectionMapping
	found := map[string]bool{}
	for _, doc := range c.Requirements.DocumentsDue {
		// A submitted file Ozav asks again for is owed, not refused: only a
		// due or rejected one is a reason the user is shown.
		if !doc.owed(false) || doc.ReasonCode != documentInvalid {
			continue
		}
		category, ok := documentCategories[doc.code()]
		if !ok {
			continue
		}
		found[category] = true
		out.details = append(out.details, documentInvalid+":"+doc.code())
	}
	for _, category := range models.KycRejectionCategories {
		if found[category] {
			out.categories = append(out.categories, category)
		}
	}
	return out
}

const codeCustomerEmailAlreadyExists = "CUSTOMER_EMAIL_ALREADY_EXISTS"

// createRefusal reads a failed create as a final refusal, when it is one:
//
//   - CUSTOMER_EMAIL_ALREADY_EXISTS: Ozav holds another customer under the
//     account's own e-mail, which nothing the user sends can change;
//   - CUSTOMER_ALREADY_EXISTS whose customer could not be adopted: the tax id
//     is another account's customer at Ozav (or one the bounded walk cannot
//     find, which no retry finds either).
//
// adoptErr is why the adoption did not happen. Only errCustomerNotAdoptable
// makes a duplicate final: a lookup that failed on the way may succeed next
// time, and so may every other failure, which keeps today's behaviour.
func createRefusal(err, adoptErr error) error {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return nil
	}
	category, ok := refusalCategory(upstream.Code, errors.Is(adoptErr, errCustomerNotAdoptable))
	if !ok {
		return nil
	}
	return &ramp.KycRefusal{Categories: []string{category}, Details: []string{upstream.Code}, Err: err}
}

// refusalCategory is the category a refused create's code stands for, and
// whether it is a refusal at all.
func refusalCategory(code string, notAdoptable bool) (string, bool) {
	switch {
	case code == codeCustomerEmailAlreadyExists:
		return models.KycRejectionNotEligible, true
	case code == codeCustomerAlreadyExists && notAdoptable:
		return models.KycRejectionTaxIDInUse, true
	}
	return "", false
}

// RejectionFromSnapshot is bridge.RejectionFromSnapshot for Ozav: a stored
// raw_status, the bare customer or its customer.updated delivery.
func RejectionFromSnapshot(raw []byte) (categories, details []string, err error) {
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("ozav: decode stored snapshot: %w", err)
	}
	if strings.HasPrefix(envelope.Type, "customer.") && len(envelope.Data) > 0 {
		raw = envelope.Data
	}
	var customer customerResponse
	if err := json.Unmarshal(raw, &customer); err != nil {
		return nil, nil, fmt.Errorf("ozav: decode stored customer: %w", err)
	}
	mapping := customer.rejection()
	return mapping.categories, mapping.details, nil
}

// storedCreateRefusal matches the detail UpstreamError.Error() wrote for a
// refused customer create — the create only, never an update.
var storedCreateRefusal = regexp.MustCompile(`^ozav: POST /customers: upstream rejected \(([A-Z_]+)\)`)

// CreateRefusalFromDetail reads a create refusal back from the detail a row
// recorded before refusals were final (UpstreamError.Error()), so the backfill
// can give those rows the rejection a create refused today gets.
//
// A stored CUSTOMER_ALREADY_EXISTS counts as not adoptable: the adoption had
// already been tried when it was recorded, and in production the one such row
// is a tax id another account holds.
func CreateRefusalFromDetail(detail string) (categories, details []string, ok bool) {
	match := storedCreateRefusal.FindStringSubmatch(detail)
	if match == nil {
		return nil, nil, false
	}
	category, ok := refusalCategory(match[1], true)
	if !ok {
		return nil, nil, false
	}
	return []string{category}, []string{match[1]}, true
}
