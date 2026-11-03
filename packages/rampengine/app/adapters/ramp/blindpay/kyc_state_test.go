package blindpay

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func limits(perTx, daily, monthly float64) customerLimit {
	return customerLimit{PerTransaction: &perTx, Daily: &daily, Monthly: &monthly}
}

func TestKycStatusFromBlindPay(t *testing.T) {
	cases := map[string]string{
		"verifying": models.KycStatusUnderReview, "pending_review": models.KycStatusUnderReview,
		"approved": models.KycStatusApproved, "approved_rfi": models.KycStatusApproved,
		"rejected": models.KycStatusRejected, "compliance_request": models.KycStatusRequiresInfo,
		"deprecated": models.KycStatusPaused, "awaiting_contract": models.KycStatusIncomplete,
		"something_new": models.KycStatusUnderReview,
	}
	for in, want := range cases {
		assert.Equal(t, want, kycStatusFromBlindPay(in), in)
	}
}

func TestBankPayoutEndorsement(t *testing.T) {
	ok := customerResponse{KycType: "standard", KycStatus: "approved", TosID: "to_1", Limit: limits(10000, 50000, 100000)}
	assert.Equal(t, models.EndorsementStatusApproved, bankPayoutEndorsement(ok).Status)

	for name, c := range map[string]customerResponse{
		"light kyc":      {KycType: "light", KycStatus: "approved", TosID: "to_1", Limit: limits(1, 1, 1)},
		"no terms":       {KycType: "standard", KycStatus: "approved", Limit: limits(1, 1, 1)},
		"zero limit":     {KycType: "standard", KycStatus: "approved", TosID: "to_1", Limit: limits(0, 1, 1)},
		"missing limits": {KycType: "standard", KycStatus: "approved", TosID: "to_1"},
	} {
		assert.Equal(t, models.EndorsementStatusIncomplete, bankPayoutEndorsement(c).Status, name)
	}
	assert.Equal(t, models.EndorsementStatusPending,
		bankPayoutEndorsement(customerResponse{KycType: "standard", KycStatus: "verifying"}).Status)
	assert.Equal(t, models.EndorsementStatusRevoked,
		bankPayoutEndorsement(customerResponse{KycType: "standard", KycStatus: "rejected"}).Status)
	assert.Equal(t, models.EndorsementBankPayout, bankPayoutEndorsement(ok).Name)
}

// A BlindPay rejection is retried with a new customer (KYC cannot be
// updated), so it must never classify as terminal.
func TestARejectionIsCorrigible(t *testing.T) {
	state := kycStateFrom(customerResponse{
		ID: "cu_1", KycType: "standard", KycStatus: "rejected",
		KycWarnings: []kycWarning{{Code: "DOCUMENT_EXPIRED", Message: "ignored"}},
	}, nil)
	assert.Equal(t, []string{models.KycRejectionDataMismatch}, state.RejectionCategories)
	assert.Equal(t, []string{"DOCUMENT_EXPIRED"}, state.RejectionDetails, "codes only, never the message")
}

func endorsementNamed(s *ramp.KycState, name string) ramp.Endorsement {
	for _, e := range s.Endorsements {
		if e.Name == name {
			return e
		}
	}
	return ramp.Endorsement{}
}

func TestPixEndorsementFollowsTheSameConditionsAsBankPayout(t *testing.T) {
	approved := customerResponse{Country: "BR", KycType: "standard", KycStatus: "approved", TosID: "to_1", Limit: limits(1, 1, 1)}
	assert.Equal(t, models.EndorsementStatusApproved, endorsementNamed(kycStateFrom(approved, nil), models.EndorsementPix).Status)
	noTos := approved
	noTos.TosID = ""
	assert.Equal(t, models.EndorsementStatusIncomplete, endorsementNamed(kycStateFrom(noTos, nil), models.EndorsementPix).Status)
	rejected := approved
	rejected.KycStatus = "rejected"
	assert.Equal(t, models.EndorsementStatusRevoked, endorsementNamed(kycStateFrom(rejected, nil), models.EndorsementPix).Status)
}

// BlindPay serves Pix to customers of Brazil only. An approved customer of
// another country, or one whose country is not stated, holds no pix
// endorsement to clear a Pix rail with: not_available, final. Its bank payout
// endorsement is untouched.
func TestPixEndorsementIsNotAvailableOutsideBrazil(t *testing.T) {
	for _, country := range []string{"US", "PT", ""} {
		t.Run(country, func(t *testing.T) {
			c := customerResponse{Country: country, KycType: "standard", KycStatus: "approved", TosID: "to_1", Limit: limits(1, 1, 1)}
			state := kycStateFrom(c, nil)
			assert.Equal(t, models.EndorsementStatusNotAvailable, endorsementNamed(state, models.EndorsementPix).Status)
			assert.Equal(t, models.EndorsementStatusApproved, endorsementNamed(state, models.EndorsementBankPayout).Status)
		})
	}
	brazilian := customerResponse{Country: "br", KycType: "standard", KycStatus: "approved", TosID: "to_1", Limit: limits(1, 1, 1)}
	assert.Equal(t, models.EndorsementStatusApproved, endorsementNamed(kycStateFrom(brazilian, nil), models.EndorsementPix).Status)
}
