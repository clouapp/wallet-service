package blindpay

import (
	"encoding/json"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func kycStatusFromBlindPay(status string) string {
	switch status {
	case "verifying", "pending_review":
		return models.KycStatusUnderReview
	case "approved", "approved_rfi":
		return models.KycStatusApproved
	case "rejected":
		return models.KycStatusRejected
	case "compliance_request":
		return models.KycStatusRequiresInfo
	case "deprecated":
		return models.KycStatusPaused
	case "awaiting_contract":
		return models.KycStatusIncomplete
	}
	// A status BlindPay adds later is read as still under review: it can
	// neither open a rail nor close one by accident.
	return models.KycStatusUnderReview
}

func positive(limit *float64) bool { return limit != nil && *limit > 0 }

// bankPayoutEndorsement is whether BlindPay would take a bank payout from
// this customer: a standard or enhanced KYC approved, its terms accepted, and
// every limit open. BlindPay states no endorsement of its own; this is the
// set of conditions its quote endpoint refuses on.
func bankPayoutEndorsement(c customerResponse) ramp.Endorsement {
	status := models.EndorsementStatusIncomplete
	switch {
	case c.KycStatus == "rejected":
		status = models.EndorsementStatusRevoked
	case c.KycStatus == "verifying" || c.KycStatus == "pending_review":
		status = models.EndorsementStatusPending
	case (c.KycType == "standard" || c.KycType == "enhanced") &&
		(c.KycStatus == "approved" || c.KycStatus == "approved_rfi") &&
		c.TosID != "" &&
		positive(c.Limit.PerTransaction) && positive(c.Limit.Daily) && positive(c.Limit.Monthly):
		status = models.EndorsementStatusApproved
	}
	return ramp.Endorsement{Name: models.EndorsementBankPayout, Status: status}
}

// pixEndorsement is whether BlindPay would take a Pix payin or payout from
// this customer. BlindPay approves a customer, not a direction, so this is the
// umbrella `pix` under the same conditions as a bank payout — for a customer
// of Brazil. Anyone else is not_available: BlindPay serves Pix to Brazilian
// residents only (RailsFor), and an approved customer of another country must
// not clear a Pix rail because routing found no declaration to refuse it on.
func pixEndorsement(c customerResponse) ramp.Endorsement {
	if !strings.EqualFold(c.Country, "BR") {
		return ramp.Endorsement{Name: models.EndorsementPix, Status: models.EndorsementStatusNotAvailable}
	}
	e := bankPayoutEndorsement(c)
	e.Name = models.EndorsementPix
	return e
}

// kycStateFrom normalizes a customer. A rejection is always corrigible
// (data_mismatch): BlindPay KYC cannot be updated, so the way forward is a new
// customer, and a terminal category would close that door. The warning codes
// are the provider's own words for the admin; their messages can restate
// submitted data and are dropped.
func kycStateFrom(c customerResponse, raw json.RawMessage) *ramp.KycState {
	tos := models.TosStatusPending
	if c.TosID != "" {
		tos = models.TosStatusApproved
	}
	state := &ramp.KycState{
		ExternalCustomerID: c.ID,
		Status:             kycStatusFromBlindPay(c.KycStatus),
		TosStatus:          tos,
		SignedAgreementID:  c.TosID,
		Endorsements:       []ramp.Endorsement{bankPayoutEndorsement(c), pixEndorsement(c)},
		CustomerEmail:      c.Email,
		Raw:                raw,
	}
	if state.Status == models.KycStatusRejected {
		state.RejectionCategories = []string{models.KycRejectionDataMismatch}
		for _, warning := range c.KycWarnings {
			if warning.Code != "" {
				state.RejectionDetails = append(state.RejectionDetails, warning.Code)
			}
		}
	}
	return state
}
