package providers

import (
	"context"
	"errors"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/mails"
)

// platformTestMailer is the settings test send. The controller calls the
// settings service, and the service calls this. Mail().Send is the delivery.
// The message is not queued.
type platformTestMailer struct{}

func (platformTestMailer) Send(ctx context.Context, to string) error {
	if ctx == nil {
		return errors.New("settings test mail: context is required")
	}
	if to == "" {
		return errors.New("settings test mail: recipient is required")
	}
	return appfacades.Mail().To([]string{to}).Send(&mails.SettingsTestMail{To: to})
}
