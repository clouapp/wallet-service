package kyc

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestPendingStepsFromMissing(t *testing.T) {
	tests := []struct {
		name    string
		missing []string
		want    []string
	}{
		{
			name:    "address and selfie",
			missing: []string{"proof_of_address", "live_selfie"},
			want:    []string{"proof_of_address", "selfie"},
		},
		{
			name:    "tax id personal",
			missing: []string{"tax_id"},
			want:    []string{"personal"},
		},
		{
			// A passport holder's pix_onramp: a CPF they do not have, and the
			// region it is not offered in. Neither is anything the wizard
			// collects, so a row stored with them asks for nothing.
			name:    "a cpf the person does not have, and a region",
			missing: []string{"has_tin_of_type_cpf", "endorsement_not_available_in_customers_region"},
			want:    nil,
		},
		{
			name:    "government id document",
			missing: []string{"government_id_document"},
			want:    []string{"document"},
		},
		{
			name:    "government id code",
			missing: []string{"government_id"},
			want:    []string{"document"},
		},
		{
			name:    "id expired code",
			missing: []string{"id_expired"},
			want:    []string{"document"},
		},
		{
			name:    "external account non UI",
			missing: []string{"external_account"},
			want:    nil,
		},
		{
			name:    "id rejection phrase",
			missing: []string{"cannot validate id"},
			want:    []string{"document"},
		},
		{
			name:    "all personal codes",
			missing: []string{"tax_id", "tax_identification", "tax_identification_number_not_compatible"},
			want:    []string{"personal"},
		},
		{
			name:    "all address codes",
			missing: []string{"po_box", "address"},
			want:    []string{"address"},
		},
		{
			name:    "all document codes",
			missing: []string{"government_id", "government_id_document", "national_id", "driver_license", "identifying_information", "id_expired"},
			want:    []string{"document"},
		},
		{
			name:    "all selfie codes",
			missing: []string{"live_selfie", "selfie", "selfie_document"},
			want:    []string{"selfie"},
		},
		{
			name:    "non UI processing codes",
			missing: []string{"account_processing", "account_provisioning", "post_processing"},
			want:    nil,
		},
		{
			name:    "id is expired phrase",
			missing: []string{"ID is expired"},
			want:    []string{"document"},
		},
		{
			name:    "normalize whitespace and dashes",
			missing: []string{"  Proof   Of   Address  ", "LIVE--SELFIE"},
			want:    []string{"proof_of_address", "selfie"},
		},
		{
			name:    "stable order dedupe",
			missing: []string{"live_selfie", "tax_id", "proof_of_address", "government_id_document", "tax_id"},
			want:    []string{"personal", "proof_of_address", "document", "selfie"},
		},
		{
			// Bridge's codes for a resident of the United States
			// (missing-requirements docs, spec §2.7).
			name: "us personal codes",
			missing: []string{"tax_identification_number", "tax_identification_number_missing",
				"database_check_failed_on_social_security_number", "min_age_18"},
			want: []string{"personal"},
		},
		{
			// Measured in the sandbox for a customer born in 1960 and cleared
			// by source_of_funds, which the profile step collects (spec §8.4).
			name:    "source of funds questionnaire",
			missing: []string{"source_of_funds_questionnaire"},
			want:    []string{"profile"},
		},
		{
			name: "us address codes",
			missing: []string{"address_of_residence", "residence_address_invalid_subdivision_code",
				"subdivision_not_ny_usa", "subdivision_not_ak_usa"},
			want: []string{"address"},
		},
		{
			name:    "pix document and selfie codes",
			missing: []string{"government_id_document_missing", "government_id_front_image_missing", "selfie_missing"},
			want:    []string{"document", "selfie"},
		},
		{
			// The app always sends the phone, as it does for Ozav's "phone".
			name:    "phone number missing opens nothing",
			missing: []string{"phone_number_missing"},
			want:    nil,
		},
		{
			name:    "unknown code ignored",
			missing: []string{"passport", "manual_review"},
			want:    nil,
		},
		{
			name:    "mixed known and unknown",
			missing: []string{"passport", "tax_id", "external_account"},
			want:    []string{"personal"},
		},
		{
			name:    "empty input",
			missing: nil,
			want:    nil,
		},
		{
			name:    "cannot validate id with extra text",
			missing: []string{"Cannot validate ID -- upload a clear photo of the full ID"},
			want:    []string{"document"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PendingStepsFromMissing(tt.missing)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeMissingText(t *testing.T) {
	require.Equal(
		t,
		"cannot validate id - upload a clear photo of the full id",
		normalizeMissingText("  Cannot   validate ID -- upload a clear photo of the full ID  "),
	)
}

// PendingStepsFromMissing was written against Bridge's requirement names. Ozav
// reports field paths on the customer instead, and of its 26 missing_fields
// codes exactly one overlapped — so a Brazilian individual missing a mother's
// name produced no pending step at all, and the wizard had no screen to open.
func TestPendingStepsFromMissingMapsOzavVocabulary(t *testing.T) {
	tests := []struct {
		name    string
		missing []string
		want    []string
	}{
		{"mother name is a personal field", []string{"mother_name"}, []string{"personal"}},
		{"date of birth is personal", []string{"date_of_birth"}, []string{"personal"}},
		{"legal name is personal", []string{"legal_name"}, []string{"personal"}},
		{"residential address is the address step", []string{"residential_address"}, []string{"address"}},
		{
			// The risk-profile fields used to be silenced here as "not the
			// gate" — measured true at the time, but the app now collects
			// them, so a provider naming one gets the step that collects it.
			// Nationality lives on the personal-data screen, not profile.
			"risk-profile fields open the profile step (and nationality personal)",
			[]string{
				"source_of_funds", "expected_monthly_volume_usd", "monthly_income_usd",
				"occupation", "nationality", "is_pep", "account_purpose",
			},
			[]string{"personal", "profile"},
		},
		{
			// Measured against the Ozav sandbox: it lists phone on a customer it
			// has approved. The app always sends one, so reopening the personal
			// step would show the user a value they already gave.
			"phone and email open no step either",
			[]string{"phone", "email"},
			nil,
		},
		{
			"a Brazilian individual missing identity, profile and address",
			[]string{"mother_name", "residential_address", "is_pep"},
			[]string{"personal", "profile", "address"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, PendingStepsFromMissing(tt.missing))
		})
	}
}

// The Ozav additions must not cost the codes that already worked.
func TestPendingStepsFromMissingKeepsBridgeVocabulary(t *testing.T) {
	require.Equal(t, []string{"document"}, PendingStepsFromMissing([]string{"government_id"}))
	require.Equal(t, []string{"selfie"}, PendingStepsFromMissing([]string{"live_selfie"}))
	require.Equal(t, []string{"personal"}, PendingStepsFromMissing([]string{"tax_id"}))
}

// Ozav names the document it is waiting on in its own CustomerDocumentType
// vocabulary, which shares no word with Bridge's. Unmapped, a customer Ozav
// could not verify produced no wizard step at all — and the app answers "no
// step" by running the whole form again, asking for the name and address it
// already holds when what is missing is a photograph.
func TestIdentityDocumentOpensTheDocumentStep(t *testing.T) {
	require.Equal(t, []string{"document"},
		PendingStepsFromMissing([]string{"identity_document"}))
}

// `proof_of_address` is a document at both providers — Bridge's
// `documents[].purposes` and Ozav's `still_required` use the same word — and
// it used to open the typed-address step, where the user retyped what the
// provider already had and nothing was uploaded.
func TestProofOfAddressOpensTheUploadStep(t *testing.T) {
	require.Equal(t, []string{"proof_of_address"},
		PendingStepsFromMissing([]string{"proof_of_address"}))
}

// The risk-profile fields used to be silenced as "not the gate". They are not
// the gate, but the app now collects them, so a provider naming one gets the
// step that collects it. Nationality lives on the personal-data screen.
func TestRiskProfileFieldsOpenTheProfileStep(t *testing.T) {
	require.Equal(t, []string{"personal", "profile"},
		PendingStepsFromMissing([]string{
			"nationality", "occupation", "source_of_funds", "monthly_income_usd",
			"expected_monthly_volume_usd", "is_pep", "account_purpose",
		}))
}

// Ozav asks every individual for a recent bank statement, and its compliance
// began chasing real customers for one (28/09/2026). It used to open nothing,
// so the only way to answer was Ozav's own portal, outside the app.
func TestBankStatementOpensTheUploadStep(t *testing.T) {
	require.Equal(t, []string{"bank_statement"},
		PendingStepsFromMissing([]string{"bank_statement"}))
}

func TestPendingStepOrderPutsProfileAfterPersonalAndProofAfterAddress(t *testing.T) {
	require.Equal(t,
		[]string{"personal", "profile", "address", "proof_of_address", "bank_statement", "document", "selfie"},
		PendingStepsFromMissing([]string{
			"selfie", "identity_document", "bank_statement", "proof_of_address", "address", "occupation", "tax_id",
		}))
}

// DocumentSteps is what the document-resubmission filter reads instead of the
// full pending-step list: personal, profile and address are application
// matters — a document call cannot satisfy any of them — so they must be
// dropped here even though they are real, current, unmet requirements.
func TestDocumentSteps(t *testing.T) {
	require.Equal(t,
		[]string{"proof_of_address", "bank_statement", "document", "selfie"},
		DocumentSteps([]string{"personal", "profile", "address", "proof_of_address", "bank_statement", "document", "selfie"}))
	require.Nil(t, DocumentSteps([]string{"personal", "profile", "address"}))
	require.Nil(t, DocumentSteps(nil))
}

// sortedPendingSteps only ever emits a step it finds in pendingStepOrder or
// models.KybSteps — a step added to documentSatisfiableSteps but to neither
// would be silently dropped by DocumentSteps rather than surfaced as a bug.
func TestDocumentSatisfiableStepsAreAllInPendingStepOrder(t *testing.T) {
	inOrder := make(map[string]struct{}, len(pendingStepOrder)+len(models.KybSteps))
	for _, step := range slices.Concat(pendingStepOrder, models.KybSteps) {
		inOrder[step] = struct{}{}
	}
	for step := range documentSatisfiableSteps {
		_, ok := inOrder[step]
		require.True(t, ok, "%q is in documentSatisfiableSteps but in no wizard's order", step)
	}
}

// The steps the rejection categories reopen are named in models in the
// wizard's own ids; one misspelled there would be dropped here in silence.
func TestRejectionCategoryStepsAreAllInPendingStepOrder(t *testing.T) {
	inOrder := make(map[string]struct{}, len(pendingStepOrder))
	for _, step := range pendingStepOrder {
		inOrder[step] = struct{}{}
	}
	for _, category := range models.KycRejectionCategories {
		for _, step := range models.KycRejectionSteps(category) {
			_, ok := inOrder[step]
			require.True(t, ok, "%q reopens %q, which is not a wizard step", category, step)
		}
	}
}

// A corrigible category is something the user owes; a terminal one is not.
func TestPendingStepsFromMissingReadsTheRejectionCategories(t *testing.T) {
	require.Equal(t, []string{"document", "selfie"},
		PendingStepsFromMissing([]string{models.KycRejectionSelfieMismatch}))
	require.Equal(t, []string{"personal", "document"},
		PendingStepsFromMissing([]string{models.KycRejectionDataMismatch}))
	require.Nil(t, PendingStepsFromMissing([]string{models.KycRejectionTaxIDInUse, models.KycRejectionNotEligible}))
}
