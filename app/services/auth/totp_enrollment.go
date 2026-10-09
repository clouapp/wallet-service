package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrUserNotFound is a signed-in user whose row cannot be read.
	ErrUserNotFound = errors.New("user not found")
	// ErrTOTPAlreadyEnabled is a setup for a user who already has 2FA on.
	ErrTOTPAlreadyEnabled = errors.New("2FA is already enabled")
	// ErrTOTPNotStarted is a confirmation with no secret set up to confirm.
	ErrTOTPNotStarted = errors.New("no TOTP secret found — call setup first")
	// ErrTOTPCodeInvalid is a confirmation code that does not match the secret.
	ErrTOTPCodeInvalid = errors.New("invalid verification code")
	// ErrTOTPNotGenerated is a TOTP secret that could not be generated.
	ErrTOTPNotGenerated = errors.New("generate the totp secret")
	// ErrTOTPNotSealed is a TOTP secret that could not be sealed for storage.
	ErrTOTPNotSealed = errors.New("seal the totp secret")
	// ErrTOTPNotSaved is a sealed TOTP secret that could not be stored.
	ErrTOTPNotSaved = errors.New("store the totp secret")
	// ErrTOTPNotOpened is a stored TOTP secret that could not be opened.
	ErrTOTPNotOpened = errors.New("open the totp secret")
	// ErrTOTPNotEnabled is a confirmed code whose 2FA could not be turned on.
	ErrTOTPNotEnabled = errors.New("enable 2fa")
	// ErrRecoveryCodesNotGenerated is a set of recovery codes that could not be made.
	ErrRecoveryCodesNotGenerated = errors.New("generate the recovery codes")
	// ErrTOTPNotDisabled is 2FA that could not be turned off.
	ErrTOTPNotDisabled = errors.New("disable 2fa")
)

// TOTPStore is the user rows TOTP enrollment reads and writes (users.Service).
type TOTPStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
	UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error
	EnableTotp(ctx context.Context, id uuid.UUID) error
	DisableTotp(ctx context.Context, id uuid.UUID) error
	DeleteRecoveryCodes(ctx context.Context, userID uuid.UUID) error
	CreateRecoveryCodes(ctx context.Context, codes []models.TotpRecoveryCode) error
}

// SecretSealer seals a TOTP secret for storage (settings.CryptSealer, the
// enc:v1: envelope the verifier opens).
type SecretSealer interface {
	Seal(plaintext string) (string, error)
}

// SecondFactorProof is what a caller brings to turn 2FA off: the current code
// or an unused recovery code. Disable reads it only when the user has 2FA on:
// a user without it has nothing to prove, and a proof that cannot be read is
// not that user's error. Read's error is returned as it is.
type SecondFactorProof interface {
	Read() (code, recoveryCode string, err error)
}

// TOTPEnrollment turns a user's TOTP second factor on and off: setup stores a
// sealed secret, confirm proves the user holds it and turns 2FA on with fresh
// recovery codes, disable turns it off and replaces the sessions.
type TOTPEnrollment struct {
	passwords *Service
	verifier  *SecondFactorVerifier
	users     TOTPStore
	sealer    SecretSealer
	sessions  *SessionIssuer
}

// EnrollmentDeps is everything TOTPEnrollment uses. Every field is required.
type EnrollmentDeps struct {
	Passwords *Service
	Verifier  *SecondFactorVerifier
	Users     TOTPStore
	Sealer    SecretSealer
	Sessions  *SessionIssuer
}

// NewTOTPEnrollment builds the enrollment flows from EnrollmentDeps.
func NewTOTPEnrollment(deps EnrollmentDeps) (*TOTPEnrollment, error) {
	if deps.Passwords == nil || deps.Verifier == nil || deps.Users == nil || deps.Sealer == nil || deps.Sessions == nil {
		return nil, errors.New("auth: totp enrollment: all dependencies are required")
	}
	return &TOTPEnrollment{
		passwords: deps.Passwords,
		verifier:  deps.Verifier,
		users:     deps.Users,
		sealer:    deps.Sealer,
		sessions:  deps.Sessions,
	}, nil
}

// TOTPSecret is a secret set up for enrollment: shown once, with its QR URL.
type TOTPSecret struct {
	Secret string
	QRURL  string
}

// TOTPConfirmed is a user whose 2FA is now on, with the recovery codes shown once.
type TOTPConfirmed struct {
	User          *models.User
	RecoveryCodes []string
}

// TOTPDisabled is a user whose 2FA is now off, with the caller's new session.
type TOTPDisabled struct {
	User   *models.User
	Tokens SessionTokens
}

// Setup generates a TOTP secret for the signed-in user and stores it sealed.
// 2FA stays off until Confirm. A user who has 2FA on is ErrTOTPAlreadyEnabled.
func (e *TOTPEnrollment) Setup(ctx context.Context, user *models.User) (TOTPSecret, error) {
	if user.TotpEnabled {
		return TOTPSecret{}, ErrTOTPAlreadyEnabled
	}
	secret, qrURL, err := e.passwords.GenerateTOTP(user.Email)
	if err != nil {
		return TOTPSecret{}, fmt.Errorf("%w: %w", ErrTOTPNotGenerated, err)
	}
	sealed, err := e.sealer.Seal(secret)
	if err != nil {
		return TOTPSecret{}, fmt.Errorf("%w: %w", ErrTOTPNotSealed, err)
	}
	if err := e.users.UpdateTotpSecret(ctx, user.ID, sealed); err != nil {
		return TOTPSecret{}, fmt.Errorf("%w: %w", ErrTOTPNotSaved, err)
	}
	return TOTPSecret{Secret: secret, QRURL: qrURL}, nil
}

// Confirm checks code against the secret Setup stored, records its step so it
// cannot also complete a login, turns 2FA on and replaces the recovery codes.
// It returns the user with 2FA on and the new codes. A recovery code write that
// fails is logged; 2FA stays on.
func (e *TOTPEnrollment) Confirm(ctx context.Context, user *models.User, code string) (TOTPConfirmed, error) {
	secret, err := e.verifier.OpenSecret(user.ID)
	if err != nil {
		return TOTPConfirmed{}, fmt.Errorf("%w: %w", ErrTOTPNotOpened, err)
	}
	if secret == "" {
		return TOTPConfirmed{}, ErrTOTPNotStarted
	}
	matched, err := e.verifier.RecordConfirmedCode(user.ID, secret, code)
	if err != nil {
		return TOTPConfirmed{}, fmt.Errorf("%w: %w", ErrTOTPNotEnabled, err)
	}
	if !matched {
		return TOTPConfirmed{}, ErrTOTPCodeInvalid
	}
	if err := e.users.EnableTotp(ctx, user.ID); err != nil {
		return TOTPConfirmed{}, fmt.Errorf("%w: %w", ErrTOTPNotEnabled, err)
	}

	codes, hashes, err := e.passwords.GenerateRecoveryCodes()
	if err != nil {
		return TOTPConfirmed{}, fmt.Errorf("%w: %w", ErrRecoveryCodesNotGenerated, err)
	}
	if err := e.users.DeleteRecoveryCodes(ctx, user.ID); err != nil {
		slog.Error("auth: confirm totp: delete recovery codes", "error", err)
	}
	recoveryCodes := make([]models.TotpRecoveryCode, 0, len(hashes))
	for _, hash := range hashes {
		recoveryCodes = append(recoveryCodes, models.TotpRecoveryCode{ID: uuid.New(), UserID: user.ID, CodeHash: hash})
	}
	if err := e.users.CreateRecoveryCodes(ctx, recoveryCodes); err != nil {
		slog.Error("auth: confirm totp: store recovery codes", "error", err)
	}

	user.TotpEnabled = true
	user.TotpSecret = ""
	return TOTPConfirmed{User: user, RecoveryCodes: codes}, nil
}

// Disable turns the user's 2FA off. A user who has it on must prove a live
// second factor first: proof is read, its code and recovery code trimmed;
// neither is ErrInvalidSecondFactor, and the verifier's refusals are returned
// as they are. Every session of the user is then replaced by a new one for
// the caller, signed with guard. A recovery code delete that fails is logged;
// 2FA stays off.
func (e *TOTPEnrollment) Disable(ctx context.Context, guard SessionGuard, userID uuid.UUID, proof SecondFactorProof) (TOTPDisabled, error) {
	user, err := e.users.FindByID(ctx, userID)
	if err != nil || user == nil {
		return TOTPDisabled{}, ErrUserNotFound
	}
	if user.TotpEnabled {
		code, recoveryCode, err := proof.Read()
		if err != nil {
			return TOTPDisabled{}, err
		}
		code, recoveryCode = strings.TrimSpace(code), strings.TrimSpace(recoveryCode)
		if code == "" && recoveryCode == "" {
			return TOTPDisabled{}, ErrInvalidSecondFactor
		}
		if err := e.verifier.Verify(user, code, recoveryCode); err != nil {
			return TOTPDisabled{}, err
		}
	}

	if err := e.users.DisableTotp(ctx, user.ID); err != nil {
		return TOTPDisabled{}, fmt.Errorf("%w: %w", ErrTOTPNotDisabled, err)
	}
	if err := e.users.DeleteRecoveryCodes(ctx, user.ID); err != nil {
		slog.Error("auth: disable totp: delete recovery codes", "error", err)
	}
	tokens, err := e.sessions.Replace(ctx, guard, user.ID)
	if err != nil {
		return TOTPDisabled{}, fmt.Errorf("%w: %w", ErrSessionsNotReplaced, err)
	}

	user.TotpEnabled = false
	user.TotpSecret = ""
	return TOTPDisabled{User: user, Tokens: tokens}, nil
}
