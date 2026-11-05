package bridge

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestKycStatusFromBridge(t *testing.T) {
	cases := []struct {
		bridge string
		want   string
	}{
		{"active", models.KycStatusApproved},
		{"not_started", models.KycStatusNotStarted},
		{"incomplete", models.KycStatusIncomplete},
		{"under_review", models.KycStatusUnderReview},
		{"rejected", models.KycStatusRejected},
		{"paused", models.KycStatusPaused},
		// Bridge-only states the spec calls out explicitly.
		{"awaiting_questionnaire", models.KycStatusRequiresInfo},
		{"awaiting_ubo", models.KycStatusRequiresInfo},
		{"offboarded", models.KycStatusRejected},
		// Anything we do not recognise must not grant and must not tell the user
		// to act on information we do not have.
		{"some_new_state", models.KycStatusUnderReview},
		{"", models.KycStatusUnderReview},
	}
	for _, c := range cases {
		require.Equal(t, c.want, kycStatusFromBridge(c.bridge), "bridge status %q", c.bridge)
	}
}

func TestTransferStatusFromBridge(t *testing.T) {
	cases := []struct {
		bridge string
		want   string
	}{
		{"awaiting_funds", models.TxStatusAwaitingFunds},
		{"funds_received", models.TxStatusFundsReceived},
		{"in_review", models.TxStatusProcessing},
		{"payment_submitted", models.TxStatusProcessing},
		{"payment_processed", models.TxStatusCompleted},
		// Money that came back is refunded, not failed: the user is whole and
		// the distinction is what the statement has to show.
		{"returned", models.TxStatusRefunded},
		{"refunded", models.TxStatusRefunded},
		{"canceled", models.TxStatusFailed},
		{"error", models.TxStatusFailed},
		{"undeliverable", models.TxStatusFailed},
		// An unknown state must not be terminal — that would freeze the order —
		// and must never be completed, which would credit one that is not.
		{"some_new_state", models.TxStatusProcessing},
		{"", models.TxStatusProcessing},
	}
	for _, c := range cases {
		require.Equal(t, c.want, transferStatusFromBridge(c.bridge), "bridge state %q", c.bridge)
	}
}

func TestEndorsementStatusFromBridge(t *testing.T) {
	cases := []struct{ bridge, want string }{
		{"approved", models.EndorsementStatusApproved},
		{"pending", models.EndorsementStatusPending},
		{"incomplete", models.EndorsementStatusIncomplete},
		{"revoked", models.EndorsementStatusRevoked},
		// The state Bridge's BRL migration guide gives pix_onramp for a
		// customer outside Brazil: final, nothing the user can send.
		{"endorsement_not_available_in_customers_region", models.EndorsementStatusNotAvailable},
		{"whatever", models.EndorsementStatusIncomplete},
		{"", models.EndorsementStatusIncomplete},
	}
	for _, c := range cases {
		require.Equal(t, c.want, endorsementStatusFromBridge(c.bridge), "bridge endorsement %q", c.bridge)
	}
}

// An approved endorsement whose requirements object is absent must not
// fabricate an empty-string requirement — the client renders that as a bullet.
func TestEndorsementRequirementsAreFlattenedInOrder(t *testing.T) {
	e := endorsementResponse{
		Name:   "pix",
		Status: "incomplete",
		Requirements: endorsementRequirements{
			Issues:  []string{"id_expired"},
			Pending: []string{"manual_review"},
			Missing: &missingRequirements{
				AllOf: []string{"proof_of_address"},
				AnyOf: []string{"passport", "national_id"},
			},
		},
	}

	require.Equal(t,
		[]string{"id_expired", "manual_review", "proof_of_address", "passport", "national_id"},
		e.flatRequirements())

	empty := endorsementResponse{Name: "pix", Status: "approved"}
	require.Empty(t, empty.flatRequirements())
}

func TestCollectRequirementsDedupes(t *testing.T) {
	resp := customerResponse{
		RequirementsDue: []string{"proof_of_address", "selfie"},
		Endorsements: []endorsementResponse{
			{Name: "pix", Requirements: endorsementRequirements{
				Issues:  []string{"selfie"}, // already listed at the customer level
				Missing: &missingRequirements{AllOf: []string{"tax_id"}},
			}},
		},
	}

	require.Equal(t, []string{"proof_of_address", "selfie", "tax_id"}, resp.requirements())
}

// regionIssue is how Bridge's endorsement guide shows an endorsement the
// customer's region cannot have: an issue on an otherwise ordinary entry.
const regionIssue = "endorsement_not_available_in_customers_region"

// An endorsement the region rules out is final: it is not pending, nothing
// the user sends changes it, so it keeps no requirement and lends none to
// the customer's.
func TestAnEndorsementNotAvailableInTheRegionIsFinal(t *testing.T) {
	cases := map[string]endorsementResponse{
		"named by an issue": {Name: "pix_onramp", Status: "incomplete",
			Requirements: endorsementRequirements{
				Issues:  []string{regionIssue},
				Missing: &missingRequirements{AllOf: []string{"proof_of_address"}},
			}},
		"named by the status": {Name: "pix_onramp", Status: regionIssue,
			Requirements: endorsementRequirements{Missing: &missingRequirements{AllOf: []string{"has_tin_of_type_cpf"}}}},
		// Read as the rejection reads issues (normalizedIssue): a stray space
		// or capital does not turn a final refusal into kyc_pending with no
		// step to take.
		"named by an issue spelled loosely": {Name: "pix_onramp", Status: "incomplete",
			Requirements: endorsementRequirements{Issues: []string{" Endorsement_Not_Available_In_Customers_Region "}}},
	}
	for name, onramp := range cases {
		t.Run(name, func(t *testing.T) {
			resp := customerResponse{Status: "active", Endorsements: []endorsementResponse{
				{Name: "pix_offramp", Status: "approved"},
				onramp,
			}}

			require.Equal(t, []models.Endorsement{
				{Name: "pix_offramp", Status: models.EndorsementStatusApproved},
				{Name: "pix_onramp", Status: models.EndorsementStatusNotAvailable},
			}, resp.endorsements())
			require.Empty(t, resp.requirements())

			row := &models.CustomerProviderKyc{KycStatus: models.KycStatusApproved}
			require.NoError(t, row.SetEndorsements(resp.endorsements()))
			require.True(t, row.RailNotAvailable(models.RailPixPayin))
			require.False(t, row.SupportsRail(models.RailPixPayin))
			require.True(t, row.SupportsRail(models.RailPixPayout))
		})
	}
}

// has_tin_of_type_cpf is what the sandbox gives a passport holder's
// pix_onramp instead of the region state the guide promises. It names a CPF
// the person does not have, which no step of the form can supply, so it stays
// on the endorsement, where the admin sees it, and leaves what the user owes. Asked by anything else, it is not the onramp's to drop.
func TestTheCPFOnlyTheOnrampAsksForIsNotOwed(t *testing.T) {
	inReview := customerResponse{Status: "under_review", Endorsements: []endorsementResponse{
		{Name: "pix_offramp", Status: "incomplete", Requirements: endorsementRequirements{
			Missing: &missingRequirements{AllOf: []string{"selfie_verification"}}}},
		{Name: "pix_onramp", Status: "incomplete", Requirements: endorsementRequirements{
			Missing: &missingRequirements{AllOf: []string{"selfie_verification", "has_tin_of_type_cpf"}}}},
	}}
	require.Equal(t, []string{"selfie_verification"}, inReview.requirements())
	require.Equal(t, []string{"selfie_verification", "has_tin_of_type_cpf"}, inReview.endorsements()[1].Requirements)

	askedElsewhere := customerResponse{Status: "incomplete", RequirementsDue: []string{"has_tin_of_type_cpf"},
		Endorsements: []endorsementResponse{{Name: "pix_onramp", Status: "incomplete", Requirements: endorsementRequirements{
			Missing: &missingRequirements{AllOf: []string{"has_tin_of_type_cpf"}}}}}}
	require.Equal(t, []string{"has_tin_of_type_cpf"}, askedElsewhere.requirements())
}

// rowFrom is the row the KYC service writes from a snapshot, as far as the
// rails are concerned.
func rowFrom(t *testing.T, state *ramp.KycState) *models.CustomerProviderKyc {
	t.Helper()
	row := &models.CustomerProviderKyc{KycStatus: state.Status}
	require.NoError(t, row.SetEndorsements(state.Endorsements))
	row.SetRequirements(state.Requirements)
	return row
}

func endorsementNamed(t *testing.T, state *ramp.KycState, name string) models.Endorsement {
	t.Helper()
	for _, e := range state.Endorsements {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no %s endorsement in %+v", name, state.Endorsements)
	return models.Endorsement{}
}

// The passport customer the sandbox answered on 07/10/2026 (probe S3, ids
// and names replaced): active, pix and pix_offramp approved, pix_onramp
// incomplete for a CPF. The umbrella pix would open the deposit; the
// directional pix_onramp is what decides it, and it is not for this person.
func TestThePassportCustomerOpensThePayoutOnly(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write(readFixture(t, "customer_passport_arg.json"))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_passport_arg"})
	require.NoError(t, err)

	require.Equal(t, models.KycStatusApproved, state.Status)
	require.Equal(t, models.EndorsementStatusApproved, endorsementNamed(t, state, models.EndorsementPix).Status)
	require.Equal(t, models.EndorsementStatusApproved, endorsementNamed(t, state, models.EndorsementPixOfframp).Status)
	onramp := endorsementNamed(t, state, models.EndorsementPixOnramp)
	require.Equal(t, models.EndorsementStatusIncomplete, onramp.Status)
	require.Equal(t, []string{"has_tin_of_type_cpf"}, onramp.Requirements)
	require.Empty(t, state.Requirements, "nothing the form asks can supply a CPF the person does not have")

	row := rowFrom(t, state)
	require.False(t, row.SupportsRail(models.RailPixPayin))
	require.True(t, row.SupportsRail(models.RailPixPayout))
	require.False(t, row.AwaitingPixEndorsementReview(), "nothing is with Bridge: the row is approved as it stands")
	// Not the region state: the row cannot tell a passport holder from a CPF
	// holder, so the deposit is refused from the declared identity instead.
	require.False(t, row.RailNotAvailable(models.RailPixPayin))
}

// A CPF holder living in Portugal (probe S4, ids and names replaced): every
// Pix endorsement approved, both rails open, and the external_account Bridge
// keeps listing is still the provider's own.
func TestTheCPFCustomerLivingAbroadOpensBothRails(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write(readFixture(t, "customer_cpf_prt.json"))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_cpf_prt"})
	require.NoError(t, err)

	require.Equal(t, models.KycStatusApproved, state.Status)
	require.Equal(t, []string{"external_account"}, state.Requirements)
	row := rowFrom(t, state)
	for _, rail := range []string{models.RailPixPayin, models.RailPixPayout} {
		require.True(t, row.SupportsRail(rail), rail)
		require.False(t, row.RailNotAvailable(rail), rail)
	}
}

// A business Bridge holds at awaiting_ubo or awaiting_questionnaire owes
// that, whether or not an endorsement names a code: the state itself is the
// requirement (API contract §7.1 — owners, activity). A person's state is
// left as it reads.
func TestABusinessAwaitingItsOwnersOrItsQuestionnaireOwesIt(t *testing.T) {
	for _, status := range []string{"awaiting_ubo", "awaiting_questionnaire"} {
		business := customerResponse{Type: "business", Status: status, RequirementsDue: []string{"business_formation"}}
		require.Equal(t, []string{status, "business_formation"}, business.requirements(), status)

		person := customerResponse{Type: "individual", Status: status}
		require.Empty(t, person.requirements(), status)
	}
}
