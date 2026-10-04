package settings

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AuthorizePlatformMailTest is the gate for POST /v1/platform/settings/mail/test.
// S1.4.6 names settings.update and mail.update. Neither is in the platform
// permission catalog, so a platform_admins row is the gate. The check writes
// no activity and does not send mail.
func (s *Service) AuthorizePlatformMailTest(ctx context.Context, actorID uuid.UUID) error {
	if ctx == nil {
		return fmt.Errorf("platform settings: context is required")
	}
	if s == nil {
		return errServiceRequired
	}
	if actorID == uuid.Nil {
		return fmt.Errorf("platform settings: actor is required")
	}
	if s.admins == nil {
		return fmt.Errorf("platform settings: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return ErrPlatformForbidden
	}
	return nil
}
