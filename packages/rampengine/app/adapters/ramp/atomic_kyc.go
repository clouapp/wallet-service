package ramp

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// AtomicKycCreator is a provider whose customer is born whole: profile,
// accepted terms, identity images, selfie and the client's IP in one create,
// with no way to add documents afterwards. The app still sends the
// application and the documents separately, so the service defers the
// application (ValidateDeferredApplication, no provider call) and creates the
// customer when the documents arrive.
//
// It is optional, like TosAttester: the assertion failing is the answer
// "this provider takes documents after the customer exists".
type AtomicKycCreator interface {
	// ValidateDeferredApplication checks what the provider will need from
	// the application and answers the state the row waits in. It never
	// calls the provider.
	ValidateDeferredApplication(app Application) (*KycState, error)
	// ValidateAtomicDocuments checks the documents and the client IP before
	// anything is staged, so a bad image is a 4xx and not a failed job.
	ValidateAtomicDocuments(docs Documents) error
	// PrepareCustomer uploads the files the create needs and builds its exact
	// body. persist is called after every upload with the checkpoint so far;
	// a call that resumes from a checkpoint reuses its handles instead of
	// uploading again. The returned body is what CreatePreparedCustomer must
	// send byte for byte.
	PrepareCustomer(ctx context.Context, submission AtomicKycSubmission, checkpoint AtomicKycCheckpoint,
		persist func(AtomicKycCheckpoint) error) (*PreparedAtomicKyc, error)
	// CreatePreparedCustomer sends the prepared body with idempotencyKey.
	// When the create succeeded but reading the new customer failed, it
	// returns the state with ExternalCustomerID set AND the error, so the
	// service can link the customer before surfacing the failure.
	CreatePreparedCustomer(ctx context.Context, idempotencyKey string, prepared PreparedAtomicKyc) (*KycState, error)
}

// AtomicKycSubmission is the transient input of one atomic create. Images
// live only here and in the encrypted staging blob; they are never persisted.
type AtomicKycSubmission struct {
	SubmissionID string
	// Reference is XIP's own id for this attempt, sent as the provider's
	// external id so support can find it on both sides.
	Reference   string
	Application Application
	Documents   Documents
}

// AtomicKycUploadedField, AtomicKycCheckpoint and PreparedAtomicKyc are
// declared in models and aliased here, like Profile: the KYC intent
// repository stores them, and app/repositories cannot import this package
// (it imports repositories). The aliases keep every caller writing ramp.*.
//
// AtomicKycUploadedField is one uploaded file: which payload field it fills,
// the digest of what was uploaded, and the provider's handle for it.
type AtomicKycUploadedField = models.AtomicKycUploadedField

// AtomicKycCheckpoint is what an interrupted prepare already uploaded.
type AtomicKycCheckpoint = models.AtomicKycCheckpoint

// PreparedAtomicKyc is the exact create body and its digest. A retry sends
// these bytes, never a rebuilt body: the provider compares retries by the
// hash of the bytes.
type PreparedAtomicKyc = models.PreparedAtomicKyc
