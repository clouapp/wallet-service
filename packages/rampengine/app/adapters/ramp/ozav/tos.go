// Ozav's terms are accepted in XIP's own flow: its page has no redirect and
// posts nothing back, so AcceptTos is the only way Ozav learns a user
// accepted, and POST /customers answers 422 TERMS_ACCEPTANCE_REQUIRED until a
// session is created, accepted and named on the create.

package ozav

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

const (
	tosSessionsPath = "/customers/terms-sessions"
	tosAcceptPath   = "/customers/terms-sessions/accept"
	// tosStatusAccepted is the only session status the customer create honours.
	tosStatusAccepted = "accepted"
)

type termsSession struct {
	// ID is returned only by the create (Ozav keeps its hash), so a replay
	// and the accept response lack it; later readers take it from our row.
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	TermsURL     string    `json:"terms_url"`
	TermsVersion string    `json:"terms_version"`
	ExpiresAt    time.Time `json:"expires_at"`
	AcceptedAt   time.Time `json:"accepted_at"`
}

// acceptedBy is who accepted, as XIP collected it.
type acceptedBy struct {
	LegalName string `json:"legal_name"`
	Email     string `json:"email,omitempty"`
}

// termsAcceptRequest carries no accepted_at: Ozav stamps the call, and one
// computed here can arrive skewed (TERMS_ACCEPTED_AT_INVALID).
type termsAcceptRequest struct {
	TermsSessionID string     `json:"terms_session_id"`
	AcceptedBy     acceptedBy `json:"accepted_by"`
	IPAddress      string     `json:"ip_address,omitempty"`
	UserAgent      string     `json:"user_agent,omitempty"`
}

// CreateTosSession opens a terms session and returns the page to present. It
// sends no Idempotency-Key on purpose: Ozav answers a replayed key with 200 and
// no token, which is issued once, while a duplicate pending session costs
// nothing. The service dedupes on the session stored on the row. The page has
// no redirect leg, so the opts are ignored.
func (a *Adapter) CreateTosSession(ctx context.Context, _ ramp.CustomerRef, _ ramp.TosOpts) (*ramp.TosSession, error) {
	var resp termsSession
	if err := a.do(ctx, Request{Method: http.MethodPost, Path: tosSessionsPath}, &resp); err != nil {
		return nil, err
	}
	// Without the token the session cannot be named on a customer; the next
	// attempt mints a fresh one.
	if resp.ID == "" {
		return nil, fmt.Errorf("ozav: terms session came back without its token: %w", ramp.ErrRetryableUpstream)
	}
	return &ramp.TosSession{
		URL:       resp.TermsURL,
		SessionID: resp.ID,
		ExpiresAt: resp.ExpiresAt,
		// No SignedAgreementID: nothing is accepted yet, and the submission
		// gate reads that field as a signature.
	}, nil
}

// AcceptTos attests that the user accepted the session's terms. Ozav keeps the
// first evidence on a repeat, so a retry is safe.
func (a *Adapter) AcceptTos(ctx context.Context, _ ramp.CustomerRef, attestation ramp.TosAttestation) error {
	body := termsAcceptRequest{
		TermsSessionID: attestation.SessionID,
		AcceptedBy:     acceptedBy{LegalName: attestation.LegalName, Email: attestation.Email},
		IPAddress:      attestation.IPAddress,
		UserAgent:      attestation.UserAgent,
	}

	var resp termsSession
	if err := a.do(ctx, Request{Method: http.MethodPost, Path: tosAcceptPath, Body: body}, &resp); err != nil {
		return err
	}
	// A 200 that does not say accepted would record a signature Ozav does not
	// hold, and the create would then be refused on terms the row calls signed.
	if resp.Status != tosStatusAccepted {
		return fmt.Errorf("ozav: terms session is %q after accept: %w", resp.Status, ramp.ErrRetryableUpstream)
	}
	return nil
}
