package account

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type AcceptedInvite struct {
	User    *models.User
	Account *models.AccountInvite
}

// AcceptInvite spends a token. A new email gets a user with a real password hash.
// An email that already has a user must be the logged-in user.
func (s *Service) AcceptInvite(ctx context.Context, rawToken, password, fullName string, sessionUser *models.User) (*models.User, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || !InviteTokenMatches(HashInviteToken(rawToken), rawToken) {
		return nil, ErrInviteInvalid
	}
	invite, err := s.invites().FindPendingByTokenHash(HashInviteToken(rawToken), time.Now())
	if err != nil || invite == nil {
		return nil, ErrInviteInvalid
	}
	users := repositories.NewUserRepository()
	existing, findErr := users.FindByEmail(invite.Email)
	if existing == nil && findErr != nil && !strings.Contains(strings.ToLower(findErr.Error()), "not found") {
		return nil, findErr
	}
	var user *models.User
	if existing != nil {
		if sessionUser == nil || !strings.EqualFold(sessionUser.Email, invite.Email) {
			return nil, ErrInviteLoginRequired
		}
		user = existing
	} else {
		if len(password) < 8 {
			return nil, ErrInvitePassword
		}
		hash, hashErr := authsvc.NewService().HashPassword(password)
		if hashErr != nil {
			return nil, hashErr
		}
		user = &models.User{
			ID:           uuid.New(),
			Email:        invite.Email,
			PasswordHash: hash,
			FullName:     strings.TrimSpace(fullName),
			Status:       "active",
		}
		if _, err := facades.Orm().Query().Exec(
			`INSERT INTO users (id, email, password_hash, full_name, status, preferences, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', '{}'::jsonb, NOW(), NOW())`,
			user.ID, user.Email, user.PasswordHash, user.FullName,
		); err != nil {
			return nil, err
		}
	}
	addedBy := invite.AccountID
	if invite.InvitedBy != nil {
		addedBy = *invite.InvitedBy
	}
	if err := s.AddUser(ctx, invite.AccountID, user.ID, invite.Role, addedBy); err != nil {
		return nil, err
	}
	if err := s.invites().MarkAccepted(invite.ID, time.Now()); err != nil {
		return nil, err
	}
	return user, nil
}

// PreviewInvite returns the public facts of a pending invite.
func (s *Service) PreviewInvite(ctx context.Context, rawToken string) (*models.AccountInvite, bool, error) {
	invite, err := s.invites().FindPendingByTokenHash(HashInviteToken(strings.TrimSpace(rawToken)), time.Now())
	if err != nil || invite == nil {
		return nil, false, ErrInviteInvalid
	}
	existing, findErr := repositories.NewUserRepository().FindByEmail(invite.Email)
	needsPassword := existing == nil || (findErr != nil && strings.Contains(strings.ToLower(findErr.Error()), "not found"))
	if existing != nil {
		needsPassword = false
	}
	return invite, needsPassword, nil
}
