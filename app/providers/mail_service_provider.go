package providers

import (
	"errors"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	frameworkmail "github.com/goravel/framework/mail"

	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/app/services/settings"
)

var (
	errMailConfigRequired = errors.New("mail: config is required")
	errMailQueueRequired  = errors.New("mail: queue is required")
)

// MailServiceProvider binds facades.Mail() to mail.Mailer: the framework SMTP
// application, sent with the mail_smtp row and the mail_delivery From header
// the settings service reads at send time. The process driver is MAIL_MAILER;
// mail_delivery.driver does not select a transport.
//
// The framework mail provider binds binding.Mail first. This provider declares
// no Relationship, so it registers after it and this Bind is the one that
// stays; a Relationship naming binding.Mail would hand the key back to the
// framework. Each resolution builds a new SMTP application, because that type
// keeps builder state.
type MailServiceProvider struct{}

func (p *MailServiceProvider) Register(app foundation.Application) {
	app.Bind(binding.Mail, func(app foundation.Application) (any, error) {
		cfg := app.MakeConfig()
		if cfg == nil {
			return nil, errMailConfigRequired
		}
		queue := app.MakeQueue()
		if queue == nil {
			return nil, errMailQueueRequired
		}
		transport, err := frameworkmail.NewApplication(cfg, queue)
		if err != nil {
			return nil, err
		}
		accountSettings, err := resolve[*settings.Service](app)
		if err != nil {
			return nil, err
		}
		return appmail.NewMailer(appmail.MailerDeps{
			Transport: transport,
			Config:    cfg,
			Settings:  accountSettings,
		}), nil
	})
}

// Boot refuses to start when the settings service the mailer reads cannot be
// built, so the failure shows at boot and not on the first send.
func (p *MailServiceProvider) Boot(app foundation.Application) {
	if _, err := resolve[*settings.Service](app); err != nil {
		panic("mail: settings reader: " + err.Error())
	}
}
