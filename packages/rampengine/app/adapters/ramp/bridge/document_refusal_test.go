package bridge

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// What Bridge answered a person's documents with no Pix endorsement to take
// the selfie (PF documents investigation, round 4, 19/10/2026).
const refusedSelfies = `{"code":"invalid_parameters","message":"Please resubmit the following parameters that are either missing or invalid","source":{"location":"body","key":{"liveness_check_selfies":"liveness check selfies may only be submitted for customers with both reliance and ARS endorsements, or a PIX endorsement"}}}`

// Bridge refusing a document it was sent is the user's to fix (or ours), never
// an outage: it is ramp.ErrDocumentRefused, which the worker records as
// document_unreadable instead of provider_unavailable. The fields it named go
// into the error for the admin; the reason, which may restate a value, does
// not.
func TestSubmitDocumentsReadsARefusedDocument(t *testing.T) {
	adapter, _ := answeringAdapter(t, http.StatusBadRequest, refusedSelfies)

	_, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"}, ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{{Type: "national_id", IssuingCountry: "BRA", ImageFront: "data:image/png;base64,AA"}},
		LivenessCheckSelfies:   []ramp.Selfie{{Image: "data:image/png;base64,AB"}},
	})

	require.ErrorIs(t, err, ramp.ErrDocumentRefused)
	require.NotErrorIs(t, err, ramp.ErrRetryableUpstream)
	require.Contains(t, err.Error(), "liveness_check_selfies")
	require.NotContains(t, err.Error(), "reliance")
}

// A business's documents, the company's or a partner's, are refused the same
// way.
func TestSubmitBusinessDocumentsReadsARefusedDocument(t *testing.T) {
	for name, docs := range map[string]ramp.Documents{
		"a partner's": {PersonDocuments: []ramp.PersonDocuments{{LocalRef: "ana", ExternalID: "ap_ana",
			Documents: []ramp.IdentifyingDocument{{Type: "passport", IssuingCountry: "ARG", ImageFront: "data:image/png;base64,AA"}}}}},
		"the company's": {CompanyDocuments: []ramp.CompanyDocument{{Purpose: ramp.CompanyDocumentFormation, File: "data:application/pdf;base64,AA"}}},
	} {
		t.Run(name, func(t *testing.T) {
			adapter, _ := answeringAdapter(t, http.StatusBadRequest, `{"code":"invalid_parameters","message":"Please resubmit","source":{"location":"body","key":{"documents":"file is too small"}}}`)

			_, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, docs)

			require.ErrorIs(t, err, ramp.ErrDocumentRefused)
		})
	}
}

// Only a refusal of what was sent is the user's: a provider in trouble stays
// a provider failure, a busy customer stays retryable, and an application
// refused the same way keeps the path it had.
func TestOnlyADocumentRefusalIsReadAsOne(t *testing.T) {
	docs := ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{{Type: "national_id", ImageFront: "data:image/png;base64,AA"}}}

	failing, _ := answeringAdapter(t, http.StatusInternalServerError, `{"code":"internal_error"}`)
	_, err := failing.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"}, docs)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.NotErrorIs(t, err, ramp.ErrDocumentRefused)

	busy, _ := answeringAdapter(t, http.StatusConflict, `{"code":"resource_state_conflict","message":"busy"}`)
	_, err = busy.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"}, docs)
	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
	require.NotErrorIs(t, err, ramp.ErrDocumentRefused)

	application, _ := answeringAdapter(t, http.StatusBadRequest, refusedSelfies)
	_, err = application.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NotErrorIs(t, err, ramp.ErrDocumentRefused)
}
