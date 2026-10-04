package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/app/services/settings"
)

// UserRepository persists users. Preferences live on the users row, not in a
// separate table.
type UserRepository struct {
	db.Base
}

// NewUserRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewUserRepository(query orm.Query) *UserRepository {
	return &UserRepository{Base: db.NewBase(query)}
}

// FindByEmail returns the user with this email, or ErrRepositoryNotFound.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	if err := r.Query(ctx).Where("email = ?", email).First(&user); err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	if user.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	if err := r.attachSealedTotp(ctx, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByID returns the user with this id, or ErrRepositoryNotFound.
func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := r.Query(ctx).Where("id = ?", id).First(&user); err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	if user.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	if err := r.attachSealedTotp(ctx, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// Create inserts a user. A nil Preferences pointer is omitted so the column
// default '{}' applies; users.preferences is NOT NULL.
func (r *UserRepository) Create(ctx context.Context, user *models.User) error {
	if user == nil {
		return fmt.Errorf("create user: user is nil")
	}
	query := r.Query(ctx)
	if user.Preferences != nil {
		if err := query.Create(user); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		return nil
	}
	if err := query.Omit("Preferences").Create(user); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	user.Preferences = &models.UserPreferences{}
	return nil
}

// UpdateDefaultAccountID sets users.default_account_id.
func (r *UserRepository) UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("default_account_id", defaultAccountID); err != nil {
		return fmt.Errorf("update user default account: %w", err)
	}
	return nil
}

// UpdateFullName sets users.full_name.
func (r *UserRepository) UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("full_name", fullName); err != nil {
		return fmt.Errorf("update user full name: %w", err)
	}
	return nil
}

// UpdatePasswordHash sets users.password_hash.
func (r *UserRepository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("password_hash", hash); err != nil {
		return fmt.Errorf("update user password hash: %w", err)
	}
	return nil
}

// UpdatePreferences sets users.preferences.
func (r *UserRepository) UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error {
	jsonBytes, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("update user preferences: %w", err)
	}
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("preferences", string(jsonBytes)); err != nil {
		return fmt.Errorf("update user preferences: %w", err)
	}
	return nil
}

// UpdateTotpSecret stores a sealed TOTP secret on mfa_credentials for the user.
// The users.totp_secret column is not written. An unsealed value is refused
// and is not included in the error.
func (r *UserRepository) UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error {
	if id == uuid.Nil {
		return fmt.Errorf("update user totp secret: user id is required")
	}
	if !settings.IsSealed(secret) {
		return fmt.Errorf("update user totp secret: value is not sealed")
	}
	present, err := r.countMFA(ctx, id)
	if err != nil {
		return err
	}
	if present > 0 {
		if _, err := r.Query(ctx).Exec(`
			UPDATE mfa_credentials
			SET secret = ?, confirmed_at = NULL, updated_at = NOW()
			WHERE subject_type = ? AND subject_id = ?`,
			secret, models.MFASubjectUsers, id); err != nil {
			return fmt.Errorf("update user totp secret: %w", err)
		}
		return nil
	}
	if _, err := r.Query(ctx).Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, last_used_counter, created_at, updated_at
		)
		SELECT ?, ?, ?, ?, COALESCE(u.totp_last_used_counter, 0), NOW(), NOW()
		FROM users AS u
		WHERE u.id = ?`,
		uuid.New(), models.MFASubjectUsers, id, secret, id); err != nil {
		return fmt.Errorf("update user totp secret: %w", err)
	}
	return nil
}

// EnableTotp sets users.totp_enabled and confirms the shared credential when
// one exists. The replay counter is left as it is.
func (r *UserRepository) EnableTotp(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("totp_enabled", true); err != nil {
		return fmt.Errorf("enable user totp: %w", err)
	}
	if _, err := r.Query(ctx).Exec(`
		UPDATE mfa_credentials
		SET confirmed_at = COALESCE(confirmed_at, NOW()), updated_at = NOW()
		WHERE subject_type = ? AND subject_id = ?`,
		models.MFASubjectUsers, id); err != nil {
		return fmt.Errorf("enable user totp: %w", err)
	}
	return nil
}

// DisableTotp clears totp_enabled, the legacy secret column, and the shared
// credential secret. The replay counter stays so a later enrollment cannot
// redeem a step that was already used.
func (r *UserRepository) DisableTotp(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update(map[string]any{
		"totp_enabled": false,
		"totp_secret":  "",
	}); err != nil {
		return fmt.Errorf("disable user totp: %w", err)
	}
	if _, err := r.Query(ctx).Exec(`
		UPDATE mfa_credentials
		SET secret = '', confirmed_at = NULL, updated_at = NOW()
		WHERE subject_type = ? AND subject_id = ?`,
		models.MFASubjectUsers, id); err != nil {
		return fmt.Errorf("disable user totp: %w", err)
	}
	return nil
}

// SealedTotp returns the enc:v1: secret and the replay step. A credential row
// wins over users.totp_secret. An empty secret means there is nothing to open.
// The legacy column is still read when the credential has not been copied.
func (r *UserRepository) SealedTotp(ctx context.Context, id uuid.UUID) (string, int64, error) {
	if id == uuid.Nil {
		return "", 0, fmt.Errorf("load totp secret: user id is required")
	}
	present, err := r.countMFA(ctx, id)
	if err != nil {
		return "", 0, err
	}
	var cred struct {
		Secret  string
		Counter int64
	}
	if present > 0 {
		if err := r.Query(ctx).Raw(`
			SELECT secret, last_used_counter AS counter
			FROM mfa_credentials
			WHERE subject_type = ? AND subject_id = ?`, models.MFASubjectUsers, id).Scan(&cred); err != nil {
			return "", 0, fmt.Errorf("load totp secret: %w", err)
		}
	}
	var legacy struct {
		Secret  string
		Counter int64
	}
	if err := r.Query(ctx).Raw(`
		SELECT COALESCE(totp_secret, '') AS secret, totp_last_used_counter AS counter
		FROM users WHERE id = ?`, id).Scan(&legacy); err != nil {
		return "", 0, fmt.Errorf("load totp secret: %w", err)
	}
	if present > 0 && cred.Secret != "" {
		return cred.Secret, cred.Counter, nil
	}
	if legacy.Secret != "" {
		return legacy.Secret, legacy.Counter, nil
	}
	if present > 0 {
		return "", cred.Counter, nil
	}
	return "", legacy.Counter, nil
}

// AdvanceTotpCounter stores counter as the last redeemed TOTP step only when
// it is newer. A credential row and the legacy users column are one counter:
// the credential is used when it exists, otherwise the column. A replay,
// including a concurrent one, reports false.
func (r *UserRepository) AdvanceTotpCounter(ctx context.Context, id uuid.UUID, counter int64) (bool, error) {
	if id == uuid.Nil {
		return false, fmt.Errorf("advance totp counter: user id is required")
	}
	present, err := r.countMFA(ctx, id)
	if err != nil {
		return false, err
	}
	if present > 0 {
		result, err := r.Query(ctx).Model(&models.MfaCredential{}).
			Where("subject_type = ? AND subject_id = ? AND last_used_counter < ?", models.MFASubjectUsers, id, counter).
			Update("last_used_counter", counter)
		if err != nil {
			return false, fmt.Errorf("advance totp counter: %w", err)
		}
		return result.RowsAffected == 1, nil
	}
	result, err := r.Query(ctx).Model(&models.User{}).Where("id = ? AND totp_last_used_counter < ?", id, counter).Update("totp_last_used_counter", counter)
	if err != nil {
		return false, fmt.Errorf("advance totp counter: %w", err)
	}
	return result.RowsAffected == 1, nil
}

func (r *UserRepository) countMFA(ctx context.Context, id uuid.UUID) (int64, error) {
	var n int64
	if err := r.Query(ctx).Raw(`
		SELECT count(*) FROM mfa_credentials
		WHERE subject_type = ? AND subject_id = ?`, models.MFASubjectUsers, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("load totp secret: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("load totp secret: count is negative")
	}
	return n, nil
}

func (r *UserRepository) attachSealedTotp(ctx context.Context, user *models.User) error {
	if user == nil || user.ID == uuid.Nil {
		return nil
	}
	secret, counter, err := r.SealedTotp(ctx, user.ID)
	if err != nil {
		return err
	}
	if secret == "" {
		return nil
	}
	user.TotpSecret = secret
	user.TotpLastUsedCounter = counter
	return nil
}

// SetSuspendedAt sets or clears the platform suspension. A nil instant clears
// it. The reason column is not written.
func (r *UserRepository) SetSuspendedAt(ctx context.Context, id uuid.UUID, at *time.Time) error {
	if id == uuid.Nil {
		return fmt.Errorf("set suspended at: user id is required")
	}
	var value any
	if at != nil {
		if at.IsZero() {
			return fmt.Errorf("set suspended at: instant is required")
		}
		value = at.UTC()
	}
	result, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("suspended_at", value)
	if err != nil {
		return fmt.Errorf("set suspended at: %w", err)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("set suspended at: user was not updated")
	}
	return nil
}

// List pages every user, newest created_at first. Equal timestamps break on
// id descending so a page is stable. limit must be positive and offset must
// not be negative.
func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]models.User, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list users: context is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list users: limit and offset are invalid")
	}
	total, err := r.Query(ctx).Model(&models.User{}).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	rows := []models.User{}
	err = r.Query(ctx).
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return rows, total, nil
}

// UpdateSessionsRevokedAt sets the session watermark. Sessions issued before it are refused.
func (r *UserRepository) UpdateSessionsRevokedAt(ctx context.Context, id uuid.UUID, at time.Time) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("sessions_revoked_at", at); err != nil {
		return fmt.Errorf("update sessions revoked at: %w", err)
	}
	return nil
}
