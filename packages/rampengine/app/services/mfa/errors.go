package mfa

import (
	"errors"
	"fmt"
)

// Domain errors for the MFA flow.
var (
	ErrMfaAlreadyEnabled  = errors.New("mfa already enabled")
	ErrMfaNotEnabled      = errors.New("mfa not enabled")
	ErrInvalidCode        = errors.New("invalid or expired code")
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrStepUpLocked is a step-up refused because the subject's attempts in
	// the window ran out. It wraps ErrInvalidCode so every caller answers it
	// exactly like a wrong code; one that must act on the lockout itself (the
	// admin login ends its challenge there) can still ask for it.
	ErrStepUpLocked = fmt.Errorf("mfa: step-up attempts exhausted: %w", ErrInvalidCode)

	// ErrStepUpUncounted is a step-up attempt that could not be counted. A cap
	// that cannot count is not being enforced, so this is the server's error,
	// never a wrong code and never a pass.
	ErrStepUpUncounted = errors.New("mfa: step-up attempt could not be counted")
)
