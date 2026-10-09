package settings

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PlatformTestMailer delivers the platform mail test with Mail().Send.
// The message is not queued. The implementation lives in the provider so
// this service does not call the mail facade.
type PlatformTestMailer interface {
	Send(ctx context.Context, to string) error
}

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

// SendPlatformMailTest delivers the platform mail test. The controller does
// not call the mailer. A send failure is a static error: the transport
// error can name the SMTP host and password, so it is not returned.
func (s *Service) SendPlatformMailTest(ctx context.Context, to string) error {
	if ctx == nil {
		return fmt.Errorf("platform settings: context is required")
	}
	if s == nil || s.testMail == nil {
		return fmt.Errorf("%w: test mail sender is required", ErrPlatformTestMail)
	}
	if to == "" {
		return fmt.Errorf("platform settings: recipient is required")
	}
	if err := s.testMail.Send(ctx, to); err != nil {
		return ErrPlatformTestMail
	}
	return nil
}
