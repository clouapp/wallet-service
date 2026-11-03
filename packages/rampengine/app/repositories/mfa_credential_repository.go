package repositories

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// MfaCredentialRepository is the persistence boundary for TOTP enrollments.
//
// Like the OTP repository, every method takes the subject rather than an id:
// `subject_id` alone does not identify anyone, and a lookup that forgets
// subject_type would hand a user an admin's secret.
type MfaCredentialRepository interface {
	// FindBySubject returns the subject's enrollment, or nil when there is none.
	FindBySubject(subject identity.Subject) (*models.MfaCredential, error)
	Save(c *models.MfaCredential) error
	// SaveWithBackupCodes saves the enrollment and replaces the subject's
	// backup codes with codes in one transaction, so a secret is never stored
	// next to the recovery codes of a previous one.
	SaveWithBackupCodes(c *models.MfaCredential, codes []*models.MfaBackupCode) error
	// DeleteWithBackupCodes removes the enrollment and its backup codes
	// together: a recovery code must not outlive the enrollment it recovers.
	DeleteWithBackupCodes(subject identity.Subject) error
	// DeleteWithBackupCodesAudited is DeleteWithBackupCodes of an end user by
	// an admin: the audit entry lands in the same transaction, and only when
	// something was removed. It holds the
	// account's row until it commits, and answers ErrUserDeleted, removing
	// nothing, for an account deleted since it was read.
	DeleteWithBackupCodesAudited(user *models.User, entry *models.AdminAuditLog) error
}

type mfaCredentialRepository struct{}

// NewMfaCredentialRepository returns a repository backed by the default ORM
// connection.
func NewMfaCredentialRepository() MfaCredentialRepository {
	return &mfaCredentialRepository{}
}

func (r *mfaCredentialRepository) FindBySubject(subject identity.Subject) (*models.MfaCredential, error) {
	return first[models.MfaCredential](facades.Orm().Query().
		Where("subject_type", subject.SubjectType()).
		Where("subject_id", subject.SubjectID()), "mfa credential repo: find by subject")
}

func (r *mfaCredentialRepository) Save(c *models.MfaCredential) error {
	if err := facades.Orm().Query().Save(c); err != nil {
		return fmt.Errorf("mfa credential repo: save: %w", err)
	}
	return nil
}

func (r *mfaCredentialRepository) SaveWithBackupCodes(c *models.MfaCredential, codes []*models.MfaBackupCode) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := tx.Save(c); err != nil {
			return err
		}
		if _, err := tx.Where("subject_type", c.SubjectType).Where("subject_id", c.SubjectID).
			Delete(&models.MfaBackupCode{}); err != nil {
			return err
		}
		if len(codes) == 0 {
			return nil
		}
		return tx.Create(&codes)
	})
	if err != nil {
		return fmt.Errorf("mfa credential repo: save with backup codes: %w", err)
	}
	return nil
}

func (r *mfaCredentialRepository) DeleteWithBackupCodes(subject identity.Subject) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		return deleteMfa(tx, subject)
	})
	if err != nil {
		return fmt.Errorf("mfa credential repo: delete with backup codes: %w", err)
	}
	return nil
}

func (r *mfaCredentialRepository) DeleteWithBackupCodesAudited(user *models.User, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		// FOR SHARE on the live row: a deletion queues behind this reset, so
		// the trail never reads a reset after the deletion it followed.
		live, err := first[models.User](tx.Where("id", user.ID).SharedLock(), "lock account")
		if err != nil {
			return err
		}
		if live == nil {
			return ErrUserDeleted
		}
		removed, err := deleteMfaCounting(tx, user)
		if err != nil {
			return err
		}
		// Nothing enrolled, nothing removed: like every admin act that
		// changes nothing, it is not recorded.
		if removed == 0 {
			return nil
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("mfa credential repo: delete with backup codes audited: %w", err)
	}
	return nil
}

// deleteMfa removes the subject's enrollment and its backup codes through tx.
func deleteMfa(tx orm.Query, subject identity.Subject) error {
	_, err := deleteMfaCounting(tx, subject)
	return err
}

// deleteMfaCounting is deleteMfa answering how many rows it removed.
func deleteMfaCounting(tx orm.Query, subject identity.Subject) (int64, error) {
	var removed int64
	for _, model := range []any{&models.MfaCredential{}, &models.MfaBackupCode{}} {
		res, err := tx.Where("subject_type", subject.SubjectType()).
			Where("subject_id", subject.SubjectID()).Delete(model)
		if err != nil {
			return 0, err
		}
		removed += res.RowsAffected
	}
	return removed, nil
}
