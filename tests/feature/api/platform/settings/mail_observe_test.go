package settings

import (
	"context"

	contractsmail "github.com/goravel/framework/contracts/mail"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/mails"
	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/app/providers/mailer"
)

// sendWelcomeObserved publishes the process mail document through the mailer
// the provider builds, then dials a transport that accepts the message.
// observe runs after the document is published and before the dial, which is
// mailer.Hooks.Observe.
func sendWelcomeObserved(observe func()) error {
	config := mailer.NewConfig(mailer.Hooks{
		Baseline: appfacades.MailBaseline,
		Write:    appfacades.WriteMailConfig,
		Restore:  appfacades.RestoreMailDocument,
		Observe:  observe,
		SMTP:     observedSMTP,
		From:     observedFrom,
	})
	facade := appmail.NewFacade(appmail.FacadeDeps{
		Mailer: appmail.NewMailer(appmail.MailerDeps{
			Config: config,
			Gate:   appfacades.GateMailSend,
		}),
		Inner: quietMail{},
	})
	return facade.To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
}

func observedSMTP(ctx context.Context) (mailer.Dial, bool, error) {
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

func observedFrom(ctx context.Context) (mailer.From, bool, error) {
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

// quietMail is the inner transport. Send accepts the message. The published
// document is already visible to Observe before Send runs.
type quietMail struct{}

func (quietMail) Attach([]string) contractsmail.Mail { return quietMail{} }
func (quietMail) Bcc([]string) contractsmail.Mail    { return quietMail{} }
func (quietMail) Cc([]string) contractsmail.Mail     { return quietMail{} }
func (quietMail) Content(contractsmail.Content) contractsmail.Mail {
	return quietMail{}
}
func (quietMail) From(contractsmail.Address) contractsmail.Mail { return quietMail{} }
func (quietMail) Headers(map[string]string) contractsmail.Mail  { return quietMail{} }
func (quietMail) Queue(...contractsmail.Mailable) error         { return nil }
func (quietMail) Send(...contractsmail.Mailable) error          { return nil }
func (quietMail) Subject(string) contractsmail.Mail             { return quietMail{} }
func (quietMail) To([]string) contractsmail.Mail                { return quietMail{} }
