package users

import (
	"context"

	"github.com/macrowallets/waas/app/models"
)

// ProfileInput is PATCH /v1/users/me. A blank FullName is left as stored.
type ProfileInput struct {
	FullName string
}

// UpdateProfile writes the profile change on the user the caller already
// loaded and returns that user with the change applied.
func (s *Service) UpdateProfile(ctx context.Context, user *models.User, in ProfileInput) (*models.User, error) {
	if in.FullName == "" {
		return user, nil
	}
	if err := s.UpdateFullName(ctx, user.ID, in.FullName); err != nil {
		return nil, err
	}
	user.FullName = in.FullName
	return user, nil
}
