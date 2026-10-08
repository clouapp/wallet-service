package account

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	audit "github.com/macrowallets/waas/packages/activitylog"
)

// AcceptInvite spends a token. A new email gets a user with a real password hash.
// An email that already has a user must be the logged-in user. The user, the
// membership, the accepted stamp and invite.accepted commit together. The
// activity metadata is the role. The raw token and its hash stay out of the trail.
func (s *Service) AcceptInvite(ctx context.Context, rawToken, password, fullName string, sessionUser *models.User) (*models.User, error) {
	if s == nil || s.invites == nil || s.users == nil || s.passwords == nil {
		return nil, errors.New("account invite stores and password hasher are required")
	}
	if err := s.requireActivity(); err != nil {
		return nil, err
	}
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || !InviteTokenMatches(HashInviteToken(rawToken), rawToken) {
		return nil, ErrInviteInvalid
	}
	var user *models.User
	err := s.activity.Within(ctx, func(ctx context.Context) error {
		invite, err := s.invites.FindPendingByTokenHash(ctx, HashInviteToken(rawToken), time.Now())
		if err != nil || invite == nil {
			return ErrInviteInvalid
		}
		existing, findErr := s.users.FindByEmail(ctx, invite.Email)
		if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
			return findErr
		}
		if existing != nil && findErr == nil {
			if sessionUser == nil || !strings.EqualFold(sessionUser.Email, invite.Email) {
				return ErrInviteLoginRequired
			}
			user = existing
		} else {
			if len(password) < 8 {
				return ErrInvitePassword
			}
			hash, hashErr := s.passwords.HashPassword(password)
			if hashErr != nil {
				return hashErr
			}
			user = &models.User{
				ID:           uuid.New(),
				Email:        invite.Email,
				PasswordHash: hash,
				FullName:     strings.TrimSpace(fullName),
				Status:       models.StatusActive,
			}
			if err := s.users.Create(ctx, user); err != nil {
				return err
			}
		}
		addedBy := invite.AccountID
		if invite.InvitedBy != nil {
			addedBy = *invite.InvitedBy
		}
		if err := s.AddUser(ctx, invite.AccountID, user.ID, invite.Role, addedBy); err != nil {
			return err
		}
		accepted := audit.WithIntent(ctx, audit.Intent{Event: activitylog.ActionInviteAccepted})
		if err := s.invites.MarkAccepted(accepted, invite.ID, time.Now()); err != nil {
			return err
		}
		return s.recordInviteAccepted(ctx, invite.AccountID, user.ID, invite)
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// PreviewInvite returns the public facts of a pending invite and whether a password is required.
func (s *Service) PreviewInvite(ctx context.Context, rawToken string) (*models.AccountInvite, bool, error) {
	if s == nil || s.invites == nil || s.users == nil {
		return nil, false, errors.New("account invite stores are required")
	}
	invite, err := s.invites.FindPendingByTokenHash(ctx, HashInviteToken(strings.TrimSpace(rawToken)), time.Now())
	if err != nil || invite == nil {
		return nil, false, ErrInviteInvalid
	}
	existing, findErr := s.users.FindByEmail(ctx, invite.Email)
	if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
		return nil, false, findErr
	}
	needsPassword := existing == nil || errors.Is(findErr, models.ErrRepositoryNotFound)
	return invite, needsPassword, nil
}
