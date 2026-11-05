package ozav

import "github.com/macrowallets/waas/packages/rampengine/app/models"

// synthesizeEndorsement builds the one endorsement Ozav's flat customer model
// implies. Approval is a property of the customer, not of a direction, so it
// is the umbrella "pix". missing_fields do not gate it: a customer with only
// risk-profile fields outstanding quotes and transfers fine (measured).
func synthesizeEndorsement(status, verificationMode string) models.Endorsement {
	endorsementStatus := models.EndorsementStatusIncomplete
	if customerKycStatus(status, verificationMode) == models.KycStatusApproved {
		endorsementStatus = models.EndorsementStatusApproved
	}
	return models.Endorsement{
		Name:   models.EndorsementPix,
		Status: endorsementStatus,
	}
}
