package bridge

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// rejectionMapping is a refusal translated onto the XIP categories, with
// Bridge's own words kept for the admin.
type rejectionMapping struct {
	categories []string
	details    []string
	// unmapped names what could not be translated, for the warning that asks
	// for it to be mapped. Codes and Bridge's generic labels only: a
	// developer_reason nobody has read may restate the customer.
	unmapped []string
}

// issueGovernmentIDFailed is the one issue that does not name its category by
// itself: Bridge raises it both for an ID it could not read (the country
// check) and for an ID whose data contradicts the account (measured on the
// production rejections of 28/09/2026). The developer_reason says which.
const issueGovernmentIDFailed = "government_id_verification_failed"

// issueCategories maps the structured codes in
// `endorsements[].requirements.issues`.
var issueCategories = map[string]string{
	"selfie_verification_failed": models.KycRejectionSelfieMismatch,
	"id_expired":                 models.KycRejectionDocumentUnreadable,
	// A US resident's SSN that failed the database check, or that Bridge
	// reads as made up (sandbox, 14/10/2026: 111-11-1111, 123-45-6789), is
	// data the user corrects; a residence Bridge does not serve is final.
	"database_check_failed_on_social_security_number": models.KycRejectionDataMismatch,
	"tax_identification_number_fake_pattern":          models.KycRejectionDataMismatch,
	"rejected_due_to_unsupported_geo":                 models.KycRejectionNotEligible,
}

// businessIssueCategories maps the codes Bridge's KYB raises on a business
// (KYB spec §7.4, from its missing-requirements and rejection-reasons guides,
// 18/10/2026), read on a business only: a person refused with one keeps the
// not_eligible a code never seen on a person gets. A business's partners'
// codes are the person ones above. Data the company states that the
// registry, the EIN check or the website contradicts — or more owners than a
// sole proprietorship has — is corrected in the form; a formation,
// ownership or nature-of-business document that failed verification is sent
// again; the rest is Bridge declining the business, which no resubmission
// changes.
var businessIssueCategories = map[string]string{
	"rejected_due_to_inaccurate_onboarding_details":            models.KycRejectionDataMismatch,
	"business_registry_no_match":                               models.KycRejectionDataMismatch,
	"business_ein_no_match":                                    models.KycRejectionDataMismatch,
	"business_website_failure":                                 models.KycRejectionDataMismatch,
	"sole_proprietorship_too_many_ubos":                        models.KycRejectionDataMismatch,
	"business_formation_document_verification_failed":          models.KycRejectionDocumentUnreadable,
	"business_ownership_document_verification_failed":          models.KycRejectionDocumentUnreadable,
	"proof_of_nature_of_business_document_verification_failed": models.KycRejectionDocumentUnreadable,
	"rejected_due_to_unsupported_business_activities":          models.KycRejectionNotEligible,
	"rejected_due_to_unidentifiable_business_entity":           models.KycRejectionNotEligible,
	"high_risk_business_activities":                            models.KycRejectionNotEligible,
	"operates_in_prohibited_countries":                         models.KycRejectionNotEligible,
	"manual_review_rejected":                                   models.KycRejectionNotEligible,
}

// duplicateCategories maps `<field>.duplicate_customer_detected` by field
// prefix: Bridge names the tax id by its hash field.
var duplicateCategories = []struct{ prefix, category string }{
	{"tax_identification_number", models.KycRejectionTaxIDInUse},
	{"phone_number", models.KycRejectionPhoneInUse},
}

const duplicateIssueSuffix = ".duplicate_customer_detected"

// reasonPhrases maps a developer_reason by a phrase it contains, lowercased.
// Each phrase is taken from a reason Bridge sent in production.
var reasonPhrases = []struct{ phrase, category string }{
	{"did not match their live portrait", models.KycRejectionSelfieMismatch},
	{"another customer with this tax id", models.KycRejectionTaxIDInUse},
	{"another customer with this phone number", models.KycRejectionPhoneInUse},
	{"government id's country did not match", models.KycRejectionDocumentUnreadable},
	{"does not match their submitted account information", models.KycRejectionDataMismatch},
}

// rejection translates a refused customer onto the XIP categories.
//
// The structured issues come first, from every endorsement; developer_reason
// is the fallback, read only when Bridge raised no issue — and to tell apart
// the two meanings of issueGovernmentIDFailed. An unmapped reason beside a
// mapped issue is therefore ignored rather than made terminal. What cannot be
// mapped at all is not_eligible, as is a rejection that gives no reason.
func (r customerResponse) rejection() rejectionMapping {
	out := rejectionMapping{details: r.developerReasons()}
	switch r.Status {
	case bridgeStatusOffboarded:
		out.categories = []string{models.KycRejectionNotEligible}
		return out
	case "rejected":
	default:
		return rejectionMapping{}
	}

	selected := map[string]bool{}
	issues := r.issues()
	for _, issue := range issues {
		categories, ok := r.issueCategory(issue)
		if !ok {
			out.unmapped = append(out.unmapped, issue)
			categories = []string{models.KycRejectionNotEligible}
		}
		for _, category := range categories {
			selected[category] = true
		}
	}
	if len(issues) == 0 {
		for _, reason := range r.RejectionReasons {
			if reason.DeveloperReason == "" {
				continue
			}
			category, ok := reasonCategory(reason.DeveloperReason)
			if !ok {
				out.unmapped = append(out.unmapped, "developer_reason: "+reason.Reason)
				category = models.KycRejectionNotEligible
			}
			selected[category] = true
		}
	}
	if len(selected) == 0 {
		out.unmapped = append(out.unmapped, "no reason given")
		selected[models.KycRejectionNotEligible] = true
	}
	for _, category := range models.KycRejectionCategories {
		if selected[category] {
			out.categories = append(out.categories, category)
		}
	}
	return out
}

// loggedRejection is rejection, warning about whatever it could not map.
func (r customerResponse) loggedRejection() rejectionMapping {
	mapping := r.rejection()
	ramp.LogUnmappedRejection(string(ramp.ProviderBridge), mapping.unmapped)
	return mapping
}

// issueCategory maps one issue code.
func (r customerResponse) issueCategory(issue string) ([]string, bool) {
	if issue == issueGovernmentIDFailed {
		return r.governmentIDCategories(), true
	}
	if category, ok := issueCategories[issue]; ok {
		return []string{category}, true
	}
	if category, ok := businessIssueCategories[issue]; ok && r.Type == customerTypeBusiness {
		return []string{category}, true
	}
	if field, ok := strings.CutSuffix(issue, duplicateIssueSuffix); ok {
		for _, duplicate := range duplicateCategories {
			if strings.HasPrefix(field, duplicate.prefix) {
				return []string{duplicate.category}, true
			}
		}
	}
	return nil, false
}

// governmentIDCategories is what the developer_reasons say a failed
// government id was: unreadable, contradicting the account, or both. With no
// reason saying either, it is a document to send again.
func (r customerResponse) governmentIDCategories() []string {
	var out []string
	for _, reason := range r.RejectionReasons {
		category, ok := reasonCategory(reason.DeveloperReason)
		if !ok {
			continue
		}
		if category == models.KycRejectionDocumentUnreadable || category == models.KycRejectionDataMismatch {
			if !slices.Contains(out, category) {
				out = append(out, category)
			}
		}
	}
	if len(out) == 0 {
		return []string{models.KycRejectionDocumentUnreadable}
	}
	return out
}

// issues lists the issue codes of every endorsement, once each. The region
// state is not one: it says where Bridge offers an endorsement, not why it
// refused the person, and read as a reason it would make every refusal of a
// customer abroad a dead end.
func (r customerResponse) issues() []string {
	var out []string
	for _, e := range r.Endorsements {
		for _, issue := range e.Requirements.Issues {
			issue = normalizedIssue(issue)
			if issue == bridgeNotAvailableInRegion {
				continue
			}
			if issue != "" && !slices.Contains(out, issue) {
				out = append(out, issue)
			}
		}
	}
	return out
}

// developerReasons is every developer_reason as Bridge sent it, once each.
func (r customerResponse) developerReasons() []string {
	var out []string
	for _, reason := range r.RejectionReasons {
		if reason.DeveloperReason != "" && !slices.Contains(out, reason.DeveloperReason) {
			out = append(out, reason.DeveloperReason)
		}
	}
	return out
}

func reasonCategory(developerReason string) (string, bool) {
	normalized := strings.ToLower(developerReason)
	for _, entry := range reasonPhrases {
		if strings.Contains(normalized, entry.phrase) {
			return entry.category, true
		}
	}
	return "", false
}

// RejectionFromSnapshot maps a customer snapshot stored before the categories
// existed: a row's raw_status, which holds either the customer.updated
// delivery or the bare customer. It is what the backfill reads, so the rows
// already rejected get the same categories a fresh sync would give them.
func RejectionFromSnapshot(raw []byte) (categories, details []string, err error) {
	var envelope struct {
		EventObject json.RawMessage `json:"event_object"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("bridge: decode stored snapshot: %w", err)
	}
	if len(envelope.EventObject) > 0 {
		raw = envelope.EventObject
	}
	var customer customerResponse
	if err := json.Unmarshal(raw, &customer); err != nil {
		return nil, nil, fmt.Errorf("bridge: decode stored customer: %w", err)
	}
	mapping := customer.rejection()
	return mapping.categories, mapping.details, nil
}
