package responses

// ErrorResponse is the shape returned for single-message error responses.
//
// Provider and PendingSteps are present only when the refusal names someone —
// today, a compliance refusal the client can act on. They travel opaque: the
// app returns the provider code on the /kyc/{provider} routes and branches on
// nothing, so adding a provider never becomes an app release.
//
// LimitBRL and LimitToken are present only on the rail-limit refusals
// (`amount_below_minimum`, `amount_above_maximum`). Which bound LimitBRL holds
// is what the error code says, so there is no min/max pair here: that would be
// four optional fields where two are always empty. LimitToken is the same
// bound in the token of the operation, and is absent wherever there was no
// quote to convert on — every on-ramp refusal, and an off-ramp whose quote
// produced nothing.
type ErrorResponse struct {
	Error        string   `json:"error"`
	Provider     string   `json:"provider,omitempty"`
	PendingSteps []string `json:"pending_steps,omitempty"`
	LimitBRL     string   `json:"limit_brl,omitempty"`
	LimitToken   string   `json:"limit_token,omitempty"`
	// LimitUSD is the bound of a dollar payout (a bank rail's limits, or a USD
	// payout rail's minimum after XIP's fee): the refusals above name it the way
	// they name LimitBRL on Pix.
	LimitUSD string `json:"limit_usd,omitempty"`
	// RetryAfterSeconds is present only on `otp_resend_too_soon`, mirroring the
	// Retry-After header for clients that do not read headers.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}
