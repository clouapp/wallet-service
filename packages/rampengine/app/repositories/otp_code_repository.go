package repositories

import (
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrOTPCodeAlreadyUsed is a redemption that lost the race for a code another
// request had already spent. Callers answer it the way they answer a wrong
// code — the caller is holding something that is no longer redeemable.
var ErrOTPCodeAlreadyUsed = errors.New("otp code already used")

// OTPCodeRepository is the persistence boundary for one-time codes, for every
// authenticatable subject.
//
// It replaces EmailVerificationRepository and AdminPasswordResetRepository,
// which were the same six queries against two tables.
//
// Every method takes the subject rather than a bare id: `otp_codes.subject_id`
// carries no foreign key, so subject_type is not decoration — it is the only
// thing separating an admin's codes from a user's at the same id. Taking the
// subject makes it impossible for a caller to supply one and forget the other.
type OTPCodeRepository interface {
	Create(c *models.OTPCode) error
	// FindActive returns the most recent outstanding, unexpired code for
	// (subject, purpose), or nil if none is outstanding. It does not filter by
	// code: the otp service compares the guess itself so it can count wrong
	// ones.
	FindActive(subject identity.Subject, purpose identity.OTPPurpose) (*models.OTPCode, error)
	// MarkUsed spends the code, or reports ErrOTPCodeAlreadyUsed when it was
	// already spent. Redemption is single-use: the caller may act on the code
	// only when this returns nil.
	MarkUsed(c *models.OTPCode) error
	InvalidateUnused(subject identity.Subject, purpose identity.OTPPurpose) error
}

type otpCodeRepository struct{}

// NewOTPCodeRepository returns an OTPCodeRepository backed by the default ORM
// connection.
func NewOTPCodeRepository() OTPCodeRepository {
	return &otpCodeRepository{}
}

func (r *otpCodeRepository) Create(c *models.OTPCode) error {
	if err := facades.Orm().Query().Create(c); err != nil {
		return fmt.Errorf("otp repo: create: %w", err)
	}
	return nil
}

// outstanding scopes a query to one subject's live codes for one purpose.
// Every read goes through it so no lookup can forget subject_type.
func (r *otpCodeRepository) outstanding(subject identity.Subject, purpose identity.OTPPurpose) orm.Query {
	return facades.Orm().Query().
		Where("subject_type", subject.SubjectType()).
		Where("subject_id", subject.SubjectID()).
		Where("purpose", purpose).
		Where("used_at IS NULL")
}

func (r *otpCodeRepository) FindActive(subject identity.Subject, purpose identity.OTPPurpose) (*models.OTPCode, error) {
	return first[models.OTPCode](r.outstanding(subject, purpose).
		Where("expires_at > ?", time.Now()).
		OrderBy("id", "desc"), "otp repo: find active")
}

func (r *otpCodeRepository) MarkUsed(c *models.OTPCode) error {
	return markOTPUsed(facades.Orm().Query(), c)
}

// markOTPUsed spends the code only while it is still unspent, the way
// MfaBackupCodeRepository spends a recovery code. The user and admin
// repositories call it inside the transaction that writes what the code
// authorized.
//
// The `used_at IS NULL` predicate is what makes redemption single-use: two
// requests carrying the same code each hold their own copy of the row after
// FindActive, and an unconditional Save told both of them they had redeemed it.
func markOTPUsed(tx orm.Query, c *models.OTPCode) error {
	now := time.Now()
	result, err := tx.Model(&models.OTPCode{}).
		Where("id", c.ID).
		Where("used_at IS NULL").
		Update("used_at", now)
	if err != nil {
		return fmt.Errorf("otp repo: mark used: %w", err)
	}
	if result.RowsAffected != 1 {
		return ErrOTPCodeAlreadyUsed
	}
	c.UsedAt = &now
	return nil
}

func (r *otpCodeRepository) InvalidateUnused(subject identity.Subject, purpose identity.OTPPurpose) error {
	_, err := r.outstanding(subject, purpose).
		Model(&models.OTPCode{}).
		Update("used_at", time.Now())
	if err != nil {
		return fmt.Errorf("otp repo: invalidate unused: %w", err)
	}
	return nil
}
