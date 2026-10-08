package users

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// ResetMFA turns one user's TOTP off and clears the secret and recovery
// codes the self-service disable path already clears. S3.4.1 names
// users.mfa.reset; a platform_admins row is the gate on this branch. An
// unknown user is ErrNotFound before the platform-admin check. A caller
// who is not a platform admin is ErrMFAForbidden only after that user is
// found. The user is not
// suspended. A reset that clears TOTP also stamps sessions_revoked_at and
// revokes that user's refresh tokens. A user whose TOTP is already clear
// succeeds and does not append another user.mfa_reset row or revoke again.
func (s *Service) ResetMFA(ctx context.Context, actorID, targetID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("reset mfa: context is required")
	}
	if actorID == uuid.Nil {
		return fmt.Errorf("reset mfa: actor is required")
	}
	if targetID == uuid.Nil {
		return fmt.Errorf("reset mfa: user id is required")
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("users service: users repository is required")
	}
	if s.admins == nil {
		return fmt.Errorf("reset mfa: platform admins are required")
	}
	if s.activity == nil {
		return fmt.Errorf("reset mfa: activity is required")
	}
	if s.recovery == nil {
		return fmt.Errorf("reset mfa: recovery codes are required")
	}
	if _, err := s.existingUser(ctx, targetID); err != nil {
		return err
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return ErrMFAForbidden
	}
	return s.activity.Within(ctx, func(ctx context.Context) error {
		return s.resetMFA(ctx, actorID, targetID)
	})
}

func (s *Service) resetMFA(ctx context.Context, actorID, targetID uuid.UUID) error {
	user, err := s.store.FindByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return ErrNotFound
		}
		return err
	}
	if user == nil || user.ID == uuid.Nil {
		return ErrNotFound
	}
	recoveryCodes, err := s.recovery.CountByUserID(ctx, targetID)
	if err != nil {
		return err
	}
	if recoveryCodes < 0 {
		return fmt.Errorf("reset mfa: recovery code count is negative")
	}
	if !totpMaterialPresent(user, recoveryCodes) {
		return nil
	}
	if s.sessions == nil {
		return fmt.Errorf("reset mfa: sessions are required")
	}
	named := audit.WithIntent(ctx, audit.Intent{Event: activitylog.ActionUserMFAReset})
	if err := s.store.DisableTotp(named, targetID); err != nil {
		return err
	}
	if err := s.recovery.DeleteByUserID(ctx, targetID); err != nil {
		return err
	}
	if err := s.appendMFAReset(ctx, actorID, targetID); err != nil {
		return err
	}
	if _, err := s.sessions.RevokeAllBy(ctx, actorID, targetID); err != nil {
		return err
	}
	return nil
}

func (s *Service) appendMFAReset(ctx context.Context, actorID, targetID uuid.UUID) error {
	meta, err := activitylog.MFAReset()
	if err != nil {
		return err
	}
	return s.activity.Append(ctx, models.AccountActivity{
		ActorUserID: actorID,
		Action:      activitylog.ActionUserMFAReset,
		TargetType:  activitylog.TargetUser,
		TargetID:    targetID.String(),
		Metadata:    meta,
	})
}

func totpMaterialPresent(user *models.User, recoveryCodes int64) bool {
	if user == nil {
		return false
	}
	if user.TotpEnabled || strings.TrimSpace(user.TotpSecret) != "" {
		return true
	}
	return recoveryCodes > 0
}
