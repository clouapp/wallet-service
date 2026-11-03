// Package mfa orchestrates TOTP enrollment and code validation for any
// authenticatable subject.
//
// It owns secret encryption, backup-code hashing, and replay protection. It
// never issues tokens — that stays with the auth services.
package mfa

import (
	"context"
	"fmt"
	"time"

	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/totp"
)

// usedTTL bounds how long a spent TOTP window is remembered in cache. It only
// has to outlive the validation skew — after that the persisted counter has
// caught up and refuses the code on its own.
const usedTTL = 90 * time.Second

// Code types accepted by the step-up validator.
const (
	CodeTypeTOTP   = "totp"
	CodeTypeBackup = "backup"
)

// Validator is the narrow view consumers gate on: is this subject enrolled,
// and does this code check out.
//
// Declared here rather than in each consumer because the admin login flow and
// the ramp step-up want exactly the same pair, and two identical interface
// declarations drift the moment one grows a method. *Service is the only
// implementation.
type Validator interface {
	Enrolled(subject identity.Subject) (bool, error)
	ValidateStepUpCode(ctx context.Context, subject identity.Subject, code, codeType string) error
}

// Service satisfies Validator.
var _ Validator = (*Service)(nil)

// Status reports a subject's MFA state.
type Status struct {
	Enrolled    bool
	ConfirmedAt *time.Time
}

// Enrollment carries the values shown exactly once, at setup time.
type Enrollment struct {
	OtpauthURI  string
	Secret      string
	BackupCodes []string
}

// StepUpLimit is how many step-up attempts a subject gets, and how long the
// window counting them runs. It is asked on every attempt, so a limit kept as a
// runtime setting applies at once.
type StepUpLimit func(ctx context.Context, subject identity.Subject) (maxAttempts int, window time.Duration)

// Service orchestrates TOTP enrollment and validation.
type Service struct {
	credRepo    repositories.MfaCredentialRepository
	backupRepo  repositories.MfaBackupCodeRepository
	params      totp.Params
	backupCount int
	stepUps     StepUpCounter
	stepUpLimit StepUpLimit
}

// NewService wires the service with its dependencies.
func NewService(
	credRepo repositories.MfaCredentialRepository,
	backupRepo repositories.MfaBackupCodeRepository,
	params totp.Params,
	backupCount int,
	stepUps StepUpCounter,
	stepUpLimit StepUpLimit,
) *Service {
	return &Service{
		credRepo:    credRepo,
		backupRepo:  backupRepo,
		params:      params,
		backupCount: backupCount,
		stepUps:     stepUps,
		stepUpLimit: stepUpLimit,
	}
}

// Status reports whether the subject has a confirmed enrollment.
func (s *Service) Status(subject identity.Subject) (Status, error) {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return Status{}, fmt.Errorf("mfa: status: %w", err)
	}
	if cred == nil {
		return Status{}, nil
	}
	return Status{Enrolled: cred.Enrolled(), ConfirmedAt: cred.ConfirmedAt}, nil
}

// Enrolled reports whether the subject has a confirmed enrollment. Callers that
// gate an action on MFA use this rather than reading a column, because with the
// enrollment in its own table "has MFA" is the presence of a confirmed row.
func (s *Service) Enrolled(subject identity.Subject) (bool, error) {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return false, fmt.Errorf("mfa: enrolled: %w", err)
	}
	return cred.Enrolled(), nil
}

// BeginSetup generates a fresh secret and backup codes for a subject without a
// confirmed enrollment, persists the encrypted secret (pending) and hashed
// codes, and returns the plaintext values exactly once. Re-calling overwrites
// any pending secret. Returns ErrMfaAlreadyEnabled once MFA is confirmed.
func (s *Service) BeginSetup(subject identity.Subject) (Enrollment, error) {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return Enrollment{}, fmt.Errorf("mfa: begin setup: %w", err)
	}
	if cred.Enrolled() {
		return Enrollment{}, ErrMfaAlreadyEnabled
	}

	uri, secret, err := totp.GenerateSecret(s.params, subject.SubjectEmail())
	if err != nil {
		return Enrollment{}, fmt.Errorf("mfa: begin setup: %w", err)
	}
	enc, err := facades.Crypt().EncryptString(secret)
	if err != nil {
		return Enrollment{}, fmt.Errorf("mfa: encrypt secret: %w", err)
	}

	plainCodes, err := totp.GenerateBackupCodes(s.backupCount)
	if err != nil {
		return Enrollment{}, fmt.Errorf("mfa: begin setup: %w", err)
	}
	codeModels, err := s.hashBackupCodes(subject, plainCodes)
	if err != nil {
		return Enrollment{}, err
	}

	if cred == nil {
		cred = &models.MfaCredential{
			SubjectType: subject.SubjectType(),
			SubjectID:   subject.SubjectID(),
		}
	}
	cred.Secret = enc
	cred.ConfirmedAt = nil
	cred.LastUsedCounter = 0

	if err := s.credRepo.SaveWithBackupCodes(cred, codeModels); err != nil {
		return Enrollment{}, fmt.Errorf("mfa: begin setup: %w", err)
	}

	return Enrollment{OtpauthURI: uri, Secret: secret, BackupCodes: plainCodes}, nil
}

// ConfirmSetup validates the first code against the pending secret and, on
// success, confirms the enrollment and records the consumed counter.
func (s *Service) ConfirmSetup(subject identity.Subject, code string) error {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return fmt.Errorf("mfa: confirm setup: %w", err)
	}
	if cred == nil {
		return ErrInvalidCode
	}
	if cred.Enrolled() {
		return ErrMfaAlreadyEnabled
	}

	counter, err := s.checkCode(subject, cred, code, -1)
	if err != nil {
		return err
	}

	now := carbon.Now().StdTime()
	cred.ConfirmedAt = &now
	cred.LastUsedCounter = counter
	if err := s.credRepo.Save(cred); err != nil {
		return fmt.Errorf("mfa: confirm setup: %w", err)
	}
	return nil
}

// ValidateStepUpCode validates a TOTP or backup code for step-up authorization
// (a payout, disabling MFA, the admin's second login factor). For TOTP it
// enforces replay protection — the counter must advance — and persists the new
// counter. For backup it marks the code used. codeType defaults to TOTP when
// empty.
//
// Every attempt is capped, for both guards, by the subject's StepUpLimit. A
// TOTP secret outlives every guess and there is nothing to burn, so the attempt
// is claimed before the code is checked: past the cap every code, the right one
// included, gets ErrStepUpLocked, which answers like a wrong code, until the
// window lapses. A right code within the cap closes the window.
func (s *Service) ValidateStepUpCode(ctx context.Context, subject identity.Subject, code, codeType string) error {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return fmt.Errorf("mfa: validate step-up: %w", err)
	}
	if !cred.Enrolled() {
		return ErrMfaNotEnabled
	}
	maxAttempts, window := s.stepUpLimit(ctx, subject)
	attempts, err := s.stepUps.Claim(subject, window)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStepUpUncounted, err)
	}
	if attempts > int64(maxAttempts) {
		facades.Log().WithContext(ctx).With(map[string]any{"subject_type": subject.SubjectType(), "subject_id": subject.SubjectID()}).
			Warning("mfa: step-up cap reached, refusing until the window lapses")
		return ErrStepUpLocked
	}
	if codeType == "" {
		codeType = CodeTypeTOTP
	}
	if codeType == CodeTypeBackup {
		err = s.validateBackupCode(subject, code)
	} else {
		err = s.validateTOTP(subject, cred, code)
	}
	if err != nil {
		return err
	}
	if err := s.stepUps.Clear(subject); err != nil {
		facades.Log().WithContext(ctx).Errorf("mfa: clear step-up attempts: %v", err)
	}
	return nil
}

// checkCode decrypts the enrollment secret, checks the code, refuses a window
// at or before minCounter and claims the window it matched. Confirmation has no
// prior counter to advance past, so ConfirmSetup passes -1.
func (s *Service) checkCode(subject identity.Subject, cred *models.MfaCredential, code string, minCounter int64) (int64, error) {
	secret, err := facades.Crypt().DecryptString(cred.Secret)
	if err != nil {
		return 0, fmt.Errorf("mfa: decrypt secret: %w", err)
	}
	// The framework clock, which a test freezes: the window a code is checked
	// against is then the one the test minted it for.
	counter, valid, err := totp.Validate(s.params, secret, code, carbon.Now().StdTime())
	if err != nil {
		return 0, fmt.Errorf("mfa: validate: %w", err)
	}
	if !valid || counter <= minCounter {
		return 0, ErrInvalidCode
	}
	if err := claimCounter(subject, counter); err != nil {
		return 0, err
	}
	return counter, nil
}

func (s *Service) validateTOTP(subject identity.Subject, cred *models.MfaCredential, code string) error {
	counter, err := s.checkCode(subject, cred, code, cred.LastUsedCounter)
	if err != nil {
		return err
	}
	cred.LastUsedCounter = counter
	if err := s.credRepo.Save(cred); err != nil {
		return fmt.Errorf("mfa: validate totp: %w", err)
	}
	return nil
}

// validateBackupCode checks a typed backup code against the subject's unused
// ones, in the shape they were generated and hashed in (see
// totp.NormalizeBackupCode), so a right code typed in lowercase or without its
// dash is still right. Input that cannot be a code is a wrong one; the caller
// has already spent the step-up attempt either way.
func (s *Service) validateBackupCode(subject identity.Subject, typed string) error {
	code, ok := totp.NormalizeBackupCode(typed)
	if !ok {
		return ErrInvalidCode
	}
	codes, err := s.backupRepo.FindUnusedBySubject(subject)
	if err != nil {
		return fmt.Errorf("mfa: validate backup: %w", err)
	}
	for i := range codes {
		if !facades.Hash().Check(code, codes[i].CodeHash) {
			continue
		}
		spent, err := s.backupRepo.MarkUsedIfUnused(codes[i].ID)
		if err != nil {
			return fmt.Errorf("mfa: validate backup: %w", err)
		}
		if !spent {
			return ErrInvalidCode
		}
		return nil
	}
	return ErrInvalidCode
}

// Disable turns MFA off after verifying the password and a valid TOTP/backup
// code. It removes the enrollment and the backup codes together.
func (s *Service) Disable(ctx context.Context, subject identity.Credentialed, password, code, codeType string) error {
	cred, err := s.credRepo.FindBySubject(subject)
	if err != nil {
		return fmt.Errorf("mfa: disable: %w", err)
	}
	if !cred.Enrolled() {
		return ErrMfaNotEnabled
	}
	if !facades.Hash().Check(password, subject.SubjectPasswordHash()) {
		return ErrInvalidCredentials
	}
	if err := s.ValidateStepUpCode(ctx, subject, code, codeType); err != nil {
		return err
	}

	if err := s.credRepo.DeleteWithBackupCodes(subject); err != nil {
		return fmt.Errorf("mfa: disable: %w", err)
	}
	return nil
}

// usedKey names the cache slot for one subject's spent TOTP window.
//
// The subject type is part of the key, not decoration: user 1 and admin 1 are
// different accounts, and a shared slot would let one of them burn the other's
// window — the code would then be refused with no way to tell why.
func usedKey(subject identity.Subject, counter int64) string {
	return fmt.Sprintf("mfa:used:%s:%d:%d", subject.SubjectType(), subject.SubjectID(), counter)
}

// claimCounter records a consumed TOTP window in cache (NX, short TTL) so the
// same code cannot be accepted twice before the persisted counter catches up.
func claimCounter(subject identity.Subject, counter int64) error {
	if !facades.Cache().Add(usedKey(subject, counter), true, usedTTL) {
		return ErrInvalidCode
	}
	return nil
}

func (s *Service) hashBackupCodes(subject identity.Subject, plain []string) ([]*models.MfaBackupCode, error) {
	out := make([]*models.MfaBackupCode, 0, len(plain))
	for _, c := range plain {
		h, err := facades.Hash().Make(c)
		if err != nil {
			return nil, fmt.Errorf("mfa: hash backup code: %w", err)
		}
		out = append(out, &models.MfaBackupCode{
			SubjectType: subject.SubjectType(),
			SubjectID:   subject.SubjectID(),
			CodeHash:    h,
		})
	}
	return out, nil
}
