package users

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// ActivityLog appends one row on the caller's transaction.
type ActivityLog interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	Append(ctx context.Context, row models.AccountActivity) error
}

// Store is the user persistence dashboard handlers still performed themselves.
type Store interface {
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
	Create(ctx context.Context, user *models.User) error
	UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error
	UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error
	UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error
	EnableTotp(ctx context.Context, id uuid.UUID) error
	DisableTotp(ctx context.Context, id uuid.UUID) error
	SetSuspendedAt(ctx context.Context, id uuid.UUID, at *time.Time) error
	// List pages every user, newest created_at first, id descending when the
	// timestamps match. The rows still hold secrets; the HTTP view drops them.
	List(ctx context.Context, limit, offset int) ([]models.User, int64, error)
}

// RecoveryStore is the TOTP recovery-code persistence.
type RecoveryStore interface {
	FindUnusedByUserID(ctx context.Context, userID uuid.UUID) ([]models.TotpRecoveryCode, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	CreateBatch(ctx context.Context, codes []models.TotpRecoveryCode) error
	DeleteByUserID(ctx context.Context, userID uuid.UUID) error
	// CountByUserID reports how many recovery rows the user still has. It
	// does not return the codes or their hashes.
	CountByUserID(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Service is the user reads and writes the dashboard handlers call.
type Service struct {
	store    Store
	recovery RecoveryStore
	activity ActivityLog
	admins   PlatformAdmins
	sessions Sessions
	clock    func() time.Time
}

// Deps is everything the user service uses. A nil Store is reported when a
// method runs, as the missing-repository error. Recovery, Activity, Admins,
// Sessions, and Clock may be nil. A nil Activity leaves DisableTotp as a
// status change with no audit row.
type Deps struct {
	Store    Store
	Recovery RecoveryStore
	Activity ActivityLog
	Admins   PlatformAdmins
	Sessions Sessions
	Clock    func() time.Time
}

// NewService builds a user service.
func NewService(deps Deps) *Service {
	return &Service{
		store:    deps.Store,
		recovery: deps.Recovery,
		activity: deps.Activity,
		admins:   deps.Admins,
		sessions: deps.Sessions,
		clock:    deps.Clock,
	}
}

func (s *Service) require(ctx context.Context, op string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("users service: users repository is required")
	}
	return nil
}

// FindByEmail returns the user for email.
func (s *Service) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	if err := s.require(ctx, "find user by email"); err != nil {
		return nil, err
	}
	return s.store.FindByEmail(ctx, email)
}

// FindByID returns the user for id.
func (s *Service) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	if err := s.require(ctx, "find user"); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, id)
}

// Create inserts a user the caller already filled in.
func (s *Service) Create(ctx context.Context, user *models.User) error {
	if err := s.require(ctx, "create user"); err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("create user: user is required")
	}
	return s.store.Create(ctx, user)
}

// UpdateDefaultAccountID sets the user's default account.
func (s *Service) UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error {
	if err := s.require(ctx, "update default account"); err != nil {
		return err
	}
	return s.store.UpdateDefaultAccountID(ctx, id, defaultAccountID)
}

// UpdateFullName sets the user's full name.
func (s *Service) UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error {
	if err := s.require(ctx, "update full name"); err != nil {
		return err
	}
	return s.store.UpdateFullName(ctx, id, fullName)
}

// UpdatePasswordHash sets the user's password hash.
func (s *Service) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	if err := s.require(ctx, "update password"); err != nil {
		return err
	}
	return s.store.UpdatePasswordHash(ctx, id, hash)
}

// UpdatePreferences replaces the user's preference document.
func (s *Service) UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error {
	if err := s.require(ctx, "update preferences"); err != nil {
		return err
	}
	return s.store.UpdatePreferences(ctx, id, prefs)
}

// UpdateTotpSecret stores the encrypted TOTP secret.
func (s *Service) UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error {
	if err := s.require(ctx, "update totp secret"); err != nil {
		return err
	}
	return s.store.UpdateTotpSecret(ctx, id, secret)
}

// EnableTotp marks TOTP as enabled.
func (s *Service) EnableTotp(ctx context.Context, id uuid.UUID) error {
	if err := s.require(ctx, "enable totp"); err != nil {
		return err
	}
	return s.store.EnableTotp(ctx, id)
}

// DisableTotp marks TOTP as disabled.
func (s *Service) DisableTotp(ctx context.Context, id uuid.UUID) error {
	if err := s.require(ctx, "disable totp"); err != nil {
		return err
	}
	if id == uuid.Nil {
		return fmt.Errorf("disable totp: user id is required")
	}
	if s.activity == nil {
		return s.store.DisableTotp(ctx, id)
	}
	return s.activity.Within(ctx, func(ctx context.Context) error {
		named := audit.WithIntent(ctx, audit.Intent{Event: activitylog.ActionUserMFAReset})
		if err := s.store.DisableTotp(named, id); err != nil {
			return err
		}
		meta, err := activitylog.MFAReset()
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: id,
			Action:      activitylog.ActionUserMFAReset,
			TargetType:  activitylog.TargetUser,
			TargetID:    id.String(),
			Metadata:    meta,
		})
	})
}

func (s *Service) requireRecovery(ctx context.Context, op string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if s == nil || s.recovery == nil {
		return fmt.Errorf("users service: recovery codes repository is required")
	}
	return nil
}

// FindUnusedRecoveryCodes returns unused recovery codes for the user.
func (s *Service) FindUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	if err := s.requireRecovery(ctx, "list recovery codes"); err != nil {
		return nil, err
	}
	return s.recovery.FindUnusedByUserID(ctx, userID)
}

// MarkRecoveryCodeUsed marks one recovery code used.
func (s *Service) MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID) error {
	if err := s.requireRecovery(ctx, "mark recovery code used"); err != nil {
		return err
	}
	return s.recovery.MarkUsed(ctx, id)
}

// CreateRecoveryCodes inserts a batch of recovery codes.
func (s *Service) CreateRecoveryCodes(ctx context.Context, codes []models.TotpRecoveryCode) error {
	if err := s.requireRecovery(ctx, "create recovery codes"); err != nil {
		return err
	}
	return s.recovery.CreateBatch(ctx, codes)
}

// DeleteRecoveryCodes removes every recovery code for the user.
func (s *Service) DeleteRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	if err := s.requireRecovery(ctx, "delete recovery codes"); err != nil {
		return err
	}
	return s.recovery.DeleteByUserID(ctx, userID)
}
