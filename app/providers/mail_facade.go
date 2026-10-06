package providers

import (
	"context"
	"errors"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	frameworkmail "github.com/goravel/framework/mail"

	appfacades "github.com/macrowallets/waas/app/facades"
	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/app/providers/mailer"
)

var (
	errMailConfigRequired = errors.New("mail: config is required")
	errMailQueueRequired  = errors.New("mail: queue is required")
)

// bindMailFacade resolves facades.Mail() to mail.Facade over *mail.Mailer and
// mailer.Config. The framework mail provider binds binding.Mail first;
// SettingsServiceProvider is registered later and has no Relationship, so
// this Bind is the one that stays. Each resolution builds a new SMTP
// application because that type keeps builder state. The process driver is
// MAIL_MAILER. mail_delivery.driver does not select a transport.
func bindMailFacade(app foundation.Application) {
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
		config := mailer.NewConfig(mailer.Hooks{
			Baseline: appfacades.MailBaseline,
			Write:    appfacades.WriteMailConfig,
			Restore:  appfacades.RestoreMailDocument,
			SMTP:     readMailSMTP,
			From:     readMailFrom,
		})
		return appmail.NewFacade(appmail.FacadeDeps{
			Mailer: appmail.NewMailer(appmail.MailerDeps{
				Config: config,
				Gate:   appfacades.GateMailSend,
			}),
			Inner: transport,
		}), nil
	})
}

func readMailSMTP(ctx context.Context) (mailer.Dial, bool, error) {
	dial, installed, err := appfacades.ReadMailDial(ctx)
	if !installed || err != nil {
		return mailer.Dial{}, installed, err
	}
	return mailer.Dial{
		Host:          dial.Host,
		Port:          dial.Port,
		Encryption:    dial.Encryption,
		Username:      dial.Username,
		Password:      dial.Password,
		UseHost:       dial.UseHost,
		UsePort:       dial.UsePort,
		UseEncryption: dial.UseEncryption,
		UseUsername:   dial.UseUsername,
		UsePassword:   dial.UsePassword,
	}, true, nil
}

func readMailFrom(ctx context.Context) (mailer.From, bool, error) {
	from, installed, err := appfacades.ReadMailFrom(ctx)
	if !installed || err != nil {
		return mailer.From{}, installed, err
	}
	return mailer.From{
		Address:    from.Address,
		Name:       from.Name,
		UseAddress: from.UseAddress,
		UseName:    from.UseName,
	}, true, nil
}
