package kyc_test

import (
	"testing"
	"time"

	"github.com/goravel/framework/support/carbon"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// overallRow is a provider row as List hands it over: marked, jsonb columns
// filled.
func overallRow(provider, status string) models.CustomerProviderKyc {
	return models.CustomerProviderKyc{
		Provider: provider, KycStatus: status,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]",
		RejectionCategories: "[]", RejectionDetails: "[]", RawStatus: "{}",
	}
}

// overallNow is the clock the table reads staleness against.
var overallNow = time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)

// submittingSince is a row marked in flight age ago, holding a customer.
func submittingSince(provider string, age time.Duration) models.CustomerProviderKyc {
	row := overallRow(provider, models.KycStatusSubmitting)
	row.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(overallNow.Add(-age)))
	return row
}

// failedAt is a row a failed submission released: back at status, the
// failure recorded, nothing named, and the documents the provider asks of
// everyone declared.
func failedAt(provider, status string, documents ...string) models.CustomerProviderKyc {
	row := overallRow(provider, status)
	row.RecordSubmissionFailure("provider_unavailable", "ozav: POST /customers/ozav_cus_1/documents: upstream rejected (INTERNAL_ERROR)")
	row.ApplicationDocuments = documents
	return row
}

func rejectedWith(provider string, categories ...string) models.CustomerProviderKyc {
	row := overallRow(provider, models.KycStatusRejected)
	row.SetRejectionCategories(categories)
	return row
}

func requiring(provider, status string, requirements ...string) models.CustomerProviderKyc {
	row := overallRow(provider, status)
	row.SetRequirements(requirements)
	return row
}

// owingApplication is a provider with no customer: the whole application,
// plus the documents it declares for one.
func owingApplication(provider string, documents ...string) models.CustomerProviderKyc {
	row := overallRow(provider, models.KycStatusNotStarted)
	row.NeedsApplication = true
	row.ApplicationDocuments = documents
	return row
}

func approvedBridgeWithEndorsements(endorsements []models.Endorsement) models.CustomerProviderKyc {
	row := overallRow(models.ProviderBridge, models.KycStatusApproved)
	if err := row.SetEndorsements(endorsements); err != nil {
		panic(err)
	}
	return row
}

// notEligibleFor marks the rails the row's provider does not open to the
// person, as list does.
func notEligibleFor(row models.CustomerProviderKyc, rails ...string) models.CustomerProviderKyc {
	row.RailsNotEligible = rails
	return row
}

// passportEndorsements is the passport customer Bridge's sandbox answered on
// 07/10/2026, as the adapter maps it.
func passportEndorsements() []models.Endorsement {
	return []models.Endorsement{
		{Name: "base", Status: models.EndorsementStatusApproved},
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
		{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusApproved},
		{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusIncomplete,
			Requirements: []string{"has_tin_of_type_cpf"}},
		{Name: "sepa", Status: models.EndorsementStatusApproved},
	}
}

// A passport row owes nothing: not the CPF its pix_onramp names, which no
// step collects, even on a row stored before the adapter dropped it.
func TestPendingStepsForAPassportRowIsNothing(t *testing.T) {
	row := approvedBridgeWithEndorsements(passportEndorsements())
	require.Empty(t, kyc.PendingStepsFor(&row))

	row.SetRequirements([]string{"has_tin_of_type_cpf"})
	require.Empty(t, kyc.PendingStepsFor(&row))
}

func TestComputeOverall(t *testing.T) {
	fullApplication := []string{"personal", "profile", "address", "document", "selfie"}
	cases := []struct {
		name            string
		rows            []models.CustomerProviderKyc
		inFlight        map[string]bool
		submittedBefore bool
		want            kyc.Overall
	}{
		{
			name: "no eligible provider",
			rows: nil,
			want: kyc.Overall{Status: kyc.OverallUnavailable},
		},
		{
			name: "a fresh user owes every provider the whole application",
			rows: []models.CustomerProviderKyc{
				owingApplication(models.ProviderBridge),
				owingApplication(models.ProviderOzav, "proof_of_address", "bank_statement"),
			},
			want: kyc.Overall{Status: kyc.OverallNotStarted, PendingSteps: []string{
				"personal", "profile", "address", "proof_of_address", "bank_statement", "document", "selfie",
			}},
		},
		{
			name: "approved anywhere is approved",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionTaxIDInUse),
				overallRow(models.ProviderOzav, models.KycStatusApproved),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			name: "bridge active with sepa but pix still pending is in review",
			rows: []models.CustomerProviderKyc{
				approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: "sepa", Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusPending},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusPending},
				}),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			name: "bridge with pix endorsements granted is approved",
			rows: []models.CustomerProviderKyc{
				approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: "sepa", Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
				}),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			name: "bridge active with sepa while pix incomplete for provisioning is in review",
			rows: []models.CustomerProviderKyc{
				approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: "sepa", Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPix, Status: models.EndorsementStatusIncomplete, Requirements: []string{"account_provisioning"}},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusIncomplete, Requirements: []string{"account_provisioning"}},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusIncomplete, Requirements: []string{"account_provisioning"}},
				}),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// Bridge's sandbox passport customer (07/10/2026): the payout is
			// granted, and the deposit is for CPF holders only. Nothing is
			// with Bridge and nothing is the user's to send.
			name: "a passport customer with the payout granted is approved",
			rows: []models.CustomerProviderKyc{
				approvedBridgeWithEndorsements(passportEndorsements()),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			// The deposit is refused a passport holder before the row is
			// read, so its endorsement pending at Bridge is not a review the
			// overview waits on: the payout is open.
			name: "a passport customer whose deposit is pending at bridge is approved",
			rows: []models.CustomerProviderKyc{
				notEligibleFor(approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusPending},
				}), models.RailPixPayin),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			name: "a passport customer whose deposit is incomplete for provider-side codes is approved",
			rows: []models.CustomerProviderKyc{
				notEligibleFor(approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusIncomplete,
						Requirements: []string{"manual_review", "account_processing"}},
				}), models.RailPixPayin),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			// Control: the payout is still the passport holder's to wait on.
			name: "a passport customer whose payout is pending at bridge is in review",
			rows: []models.CustomerProviderKyc{
				notEligibleFor(approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusPending},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusPending},
				}), models.RailPixPayin),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// The directional endorsement decides each rail, so the umbrella
			// still pending beside them is not a review anything waits on.
			name: "a pending umbrella beside directionals that decided is approved",
			rows: []models.CustomerProviderKyc{
				approvedBridgeWithEndorsements([]models.Endorsement{
					{Name: models.EndorsementPix, Status: models.EndorsementStatusPending},
					{Name: models.EndorsementPixOfframp, Status: models.EndorsementStatusApproved},
					{Name: models.EndorsementPixOnramp, Status: models.EndorsementStatusNotAvailable},
				}),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			name: "a terminal provider and one under review",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionTaxIDInUse),
				overallRow(models.ProviderOzav, models.KycStatusUnderReview),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// If Ozav approves, the user never hears about the selfie.
			name: "a corrigible rejection waits while another provider reviews",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionSelfieMismatch),
				overallRow(models.ProviderOzav, models.KycStatusUnderReview),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// 23/10 customer 74: Ozav asked for a new proof of address while
			// Bridge sat on a manual review (pending_rfi). The review decides
			// nothing the user can speed up, so the user must still be told.
			name: "a provider asking for a document outranks another under review",
			rows: []models.CustomerProviderKyc{
				requiring(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
				overallRow(models.ProviderBridge, models.KycStatusUnderReview),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{"proof_of_address"}},
		},
		{
			// Ozav kept the row incomplete and said why: the file sent was not
			// a proof of address. The app words it from the category and does
			// not repeat the step it already explains.
			name: "a provider asking again for a document says why",
			rows: func() []models.CustomerProviderKyc {
				ozav := requiring(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address")
				ozav.SetRejectionCategories([]string{models.KycRejectionProofOfAddressInvalid})
				return []models.CustomerProviderKyc{ozav, overallRow(models.ProviderBridge, models.KycStatusUnderReview)}
			}(),
			submittedBefore: true,
			want: kyc.Overall{
				Status:       kyc.OverallActionRequired,
				Categories:   []string{models.KycRejectionProofOfAddressInvalid},
				PendingSteps: []string{"proof_of_address"},
			},
		},
		{
			name: "approved anywhere still outranks a provider asking for a document",
			rows: []models.CustomerProviderKyc{
				requiring(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
				overallRow(models.ProviderBridge, models.KycStatusApproved),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallApproved},
		},
		{
			name: "a provider asking for a document that is itself in flight is in review",
			rows: []models.CustomerProviderKyc{
				requiring(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
				overallRow(models.ProviderBridge, models.KycStatusUnderReview),
			},
			inFlight:        map[string]bool{models.ProviderOzav: true},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// The documents never reached Ozav: nothing is with the provider,
			// whatever Bridge is doing.
			name: "documents that failed at the worker outrank another provider under review",
			rows: []models.CustomerProviderKyc{
				failedAt(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
				overallRow(models.ProviderBridge, models.KycStatusUnderReview),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{
				"proof_of_address", "document", "selfie",
			}},
		},
		{
			// 23/10 10:55 UTC: the Ozav create came back 409 and the row went
			// back to not_started with the failure recorded.
			name: "an application that failed outranks another provider under review",
			rows: func() []models.CustomerProviderKyc {
				ozav := owingApplication(models.ProviderOzav)
				ozav.RecordSubmissionFailure("provider_unavailable", "ozav: POST /customers: upstream rejected (http_409)")
				return []models.CustomerProviderKyc{ozav, overallRow(models.ProviderBridge, models.KycStatusUnderReview)}
			}(),
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: fullApplication},
		},
		{
			// A provider launched after the user applied elsewhere: nothing
			// was refused or asked, so the wait on the first one goes on.
			name: "a provider the user was never sent to does not interrupt a review",
			rows: []models.CustomerProviderKyc{
				owingApplication(models.ProviderOzav, "proof_of_address"),
				overallRow(models.ProviderBridge, models.KycStatusUnderReview),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			name: "a terminal provider and one asking for a document",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionTaxIDInUse, models.KycRejectionSelfieMismatch),
				requiring(models.ProviderOzav, models.KycStatusIncomplete, "identity_document"),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{"document"}},
		},
		{
			name: "every provider terminal, one over the tax id",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionTaxIDInUse, models.KycRejectionSelfieMismatch),
				rejectedWith(models.ProviderOzav, models.KycRejectionNotEligible),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallRejected,
				Categories: []string{models.KycRejectionTaxIDInUse}},
		},
		{
			name: "every provider terminal for other reasons",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionNotEligible, models.KycRejectionPhoneInUse),
				rejectedWith(models.ProviderOzav, models.KycRejectionNotEligible),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallRejected,
				Categories: []string{models.KycRejectionNotEligible}},
		},
		{
			// Nothing classified the refusal and nothing names a step: there
			// is nothing the user can send.
			name: "a rejection with no category and nothing to send is terminal",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallRejected,
				Categories: []string{models.KycRejectionNotEligible}},
		},
		{
			// A row nothing classified keeps the steps it named before the
			// categories existed.
			name: "a rejection with no category asks for what it still names",
			rows: func() []models.CustomerProviderKyc {
				row := rejectedWith(models.ProviderBridge)
				row.SetRejectionReasons([]string{"Cannot validate ID"})
				return []models.CustomerProviderKyc{row}
			}(),
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{"document"}},
		},
		{
			// Ozav enabled after Bridge refused the user for good: Ozav owes
			// the application, and it is the user's to send.
			name: "a terminal provider and one never touched",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionTaxIDInUse),
				owingApplication(models.ProviderOzav, "proof_of_address", "bank_statement"),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{
				"personal", "profile", "address", "proof_of_address", "bank_statement", "document", "selfie",
			}},
		},
		{
			name: "pending steps with no category behind them",
			rows: []models.CustomerProviderKyc{
				requiring(models.ProviderBridge, models.KycStatusIncomplete, "live_selfie"),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{"selfie"}},
		},
		{
			name: "the union of every provider's steps, in wizard order",
			rows: []models.CustomerProviderKyc{
				rejectedWith(models.ProviderBridge, models.KycRejectionSelfieMismatch, models.KycRejectionPhoneInUse),
				requiring(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address"),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallActionRequired,
				Categories:   []string{models.KycRejectionSelfieMismatch, models.KycRejectionPhoneInUse},
				PendingSteps: []string{"personal", "proof_of_address", "document", "selfie"}},
		},
		{
			// A rejected row is asked for its categories' steps, not for what
			// its requirements still name.
			name: "a corrigible rejection asks for its categories' steps",
			rows: func() []models.CustomerProviderKyc {
				row := rejectedWith(models.ProviderBridge, models.KycRejectionDataMismatch)
				row.SetRequirements([]string{"live_selfie"})
				return []models.CustomerProviderKyc{row}
			}(),
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallActionRequired,
				Categories:   []string{models.KycRejectionDataMismatch},
				PendingSteps: []string{"personal", "document"}},
		},
		{
			// 28/09 18:04:28 UTC: right after the application and the
			// documents were sent, Bridge reads incomplete asking for the
			// document and the selfie still in the queue.
			name: "a submission in flight is working whatever the row says",
			rows: []models.CustomerProviderKyc{
				requiring(models.ProviderBridge, models.KycStatusIncomplete, "government_id", "live_selfie"),
			},
			inFlight:        map[string]bool{models.ProviderBridge: true},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			name: "a submission on its way to the provider",
			rows: []models.CustomerProviderKyc{
				overallRow(models.ProviderBridge, models.KycStatusSubmitting),
				owingApplication(models.ProviderOzav),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			name: "a paused provider",
			rows: []models.CustomerProviderKyc{
				overallRow(models.ProviderBridge, models.KycStatusPaused),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// The documents failed at the worker: the row went back to
			// incomplete with the failure recorded and nothing named, since
			// what Ozav still wants is only read on a sync. None of it is with
			// the provider, so it is the user's to send again.
			name: "documents that failed at the worker are the user's to send again",
			rows: []models.CustomerProviderKyc{
				failedAt(models.ProviderOzav, models.KycStatusIncomplete, "proof_of_address", "bank_statement"),
			},
			submittedBefore: true,
			want: kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{
				"proof_of_address", "bank_statement", "document", "selfie",
			}},
		},
		{
			// Ozav pending with nothing missing and nothing failed: it holds
			// everything it asked for.
			name: "an incomplete provider naming nothing, with nothing failed, is with the provider",
			rows: []models.CustomerProviderKyc{
				overallRow(models.ProviderOzav, models.KycStatusIncomplete),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// The database queue never resumes a reserved job, and a worker
			// that failed to load the row never released it: past the staging
			// TTL the payload is gone and nothing is on its way.
			name: "a submission stuck past the staging TTL is not in review",
			rows: []models.CustomerProviderKyc{
				submittingSince(models.ProviderBridge, 20*time.Minute),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: []string{"document", "selfie"}},
		},
		{
			// With no customer behind it, what was on its way was the
			// application, and marking reads the stuck row as owing it.
			name: "a stuck submission with no customer owes the application",
			rows: func() []models.CustomerProviderKyc {
				row := submittingSince(models.ProviderBridge, 20*time.Minute)
				row.NeedsApplication = true
				return []models.CustomerProviderKyc{row}
			}(),
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: fullApplication},
		},
		{
			name: "a submission within the staging TTL is still in review",
			rows: []models.CustomerProviderKyc{
				submittingSince(models.ProviderBridge, time.Minute),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			name: "a stuck row with a submission in flight is in review",
			rows: []models.CustomerProviderKyc{
				submittingSince(models.ProviderBridge, 20*time.Minute),
			},
			inFlight:        map[string]bool{models.ProviderBridge: true},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallInReview},
		},
		{
			// The create failed and the row went back to not_started: the
			// user has been through the wizard and is owed a way back in.
			name: "owing the application after submitting before",
			rows: []models.CustomerProviderKyc{
				owingApplication(models.ProviderBridge),
			},
			submittedBefore: true,
			want:            kyc.Overall{Status: kyc.OverallActionRequired, PendingSteps: fullApplication},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, kyc.ComputeOverall(c.rows, c.inFlight, c.submittedBefore, overallNow))
		})
	}
}

// Terminal is the one classification the overview, the listing's `terminal`
// and every path that sends a provider anything read.
func TestTerminal(t *testing.T) {
	withReasons := rejectedWith(models.ProviderBridge)
	withReasons.SetRejectionReasons([]string{"Cannot validate ID"})
	owingApplication := rejectedWith(models.ProviderOzav)
	owingApplication.NeedsApplication = true
	cases := []struct {
		name string
		row  models.CustomerProviderKyc
		want bool
	}{
		{"a terminal category", rejectedWith(models.ProviderBridge, models.KycRejectionSelfieMismatch, models.KycRejectionTaxIDInUse), true},
		{"only corrigible categories", rejectedWith(models.ProviderBridge, models.KycRejectionSelfieMismatch), false},
		// Legacy rows no adapter classified: terminal only when nothing is
		// left to send them.
		{"no category, a step still named", withReasons, false},
		{"no category, the application still owed", owingApplication, false},
		{"no category and nothing to send", rejectedWith(models.ProviderBridge), true},
		{"not rejected", overallRow(models.ProviderBridge, models.KycStatusIncomplete), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, kyc.Terminal(&c.row))
		})
	}
}

// A document the provider refused (the worker recorded document_unreadable)
// reads as a correction the user makes, worded by its category, with the
// document steps reopened -- a person's or a company's -- never as an outage
// or a rejection.
func TestComputeOverallAfterARefusedDocument(t *testing.T) {
	refused := func(business bool) models.CustomerProviderKyc {
		row := overallRow(models.ProviderBridge, models.KycStatusIncomplete)
		row.Business = business
		row.RecordSubmissionFailure(models.KycRejectionDocumentUnreadable, "bridge: PUT /customers/cus_1: upstream rejected (invalid_parameters: liveness_check_selfies)")
		row.SetRejectionCategories([]string{models.KycRejectionDocumentUnreadable})
		return row
	}

	person := kyc.ComputeOverall([]models.CustomerProviderKyc{refused(false)}, nil, true, overallNow)
	require.Equal(t, kyc.OverallActionRequired, person.Status)
	require.Equal(t, []string{models.KycRejectionDocumentUnreadable}, person.Categories)
	require.Equal(t, []string{"document"}, person.PendingSteps, "the steps its category reopens")

	company := kyc.ComputeOverall([]models.CustomerProviderKyc{refused(true)}, nil, true, overallNow)
	require.Equal(t, kyc.OverallActionRequired, company.Status)
	require.Equal(t, []string{models.KycRejectionDocumentUnreadable}, company.Categories)
	require.Equal(t, []string{models.KybStepOwners, models.KybStepCompanyDocuments}, company.PendingSteps)
}
