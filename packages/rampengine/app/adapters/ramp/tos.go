package ramp

import "context"

// TosAttester is a provider whose terms are accepted in XIP's own flow rather
// than on a page it hosts.
//
// The two shapes are not variations of one call. A hosted provider issues the
// page, watches the user sign it there, and hands back an agreement id; nothing
// on this side witnesses the acceptance. An attesting provider hands over a
// document and a session and takes *our word* that the user accepted it — the
// call below is that word, and it carries the evidence behind it.
//
// It is deliberately not part of KycAdapter, following VirtualAccountAdapter:
// only Ozav attests today, and folding it into the fat interface would make
// Bridge, MutualPay and both stubs implement a refusal nobody will ask them
// for. The service type-asserts it off a resolved adapter, and the assertion
// failing *is* the answer "this provider hosts its own terms".
type TosAttester interface {
	Code() ProviderCode
	// AcceptTos records that the user accepted the session's terms.
	//
	// It must be called only after the user actually accepted. The provider
	// stores this against the terms version that was in force when the session
	// was created, and cannot distinguish our attestation from a signature it
	// witnessed itself — which is exactly why the caller, not the adapter,
	// owns the question of whether a real person really did tick the box.
	AcceptTos(ctx context.Context, ref CustomerRef, attestation TosAttestation) error
}

// TosAttestation is the evidence behind an acceptance XIP collected itself.
//
// AcceptedAt is deliberately absent. The provider stamps the acceptance at the
// time of the call, which is within a second of the user's tap, and a
// timestamp we compute instead is a timestamp that can arrive skewed or in the
// future — a documented refusal (TERMS_ACCEPTED_AT_INVALID) bought for
// nothing. Our own record of the tick lives on the KYC row.
type TosAttestation struct {
	// SessionID is the token the provider issued when the session was created.
	// It is read back from the row, never from the client: the client never
	// sees it.
	SessionID string
	// LegalName is who accepted, as they entered it. It must be the same
	// string the application later carries, or the provider sees two people.
	LegalName string
	Email     string
	// IPAddress and UserAgent are the evidence of where the acceptance came
	// from. Both are best-effort: a provider that wants them refuses nothing
	// when they are absent, and inventing values would be worse than omitting.
	IPAddress string
	UserAgent string
}
