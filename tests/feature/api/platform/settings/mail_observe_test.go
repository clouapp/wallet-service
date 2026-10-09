package settings

import (
	"context"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/mails"
	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/app/services/settings"
)

// sendWelcomeObserved sends a welcome mail through the settings mailer, with
// the bound settings service and the process config, over a transport that
// accepts the message. observe runs where the dial would: after the send's
// document is published on the process config and before it is restored.
func sendWelcomeObserved(observe func()) error {
	return sendWelcomeObservedWith(container.MustMake[*settings.Service](), observe)
}

// sendWelcomeObservedWith is sendWelcomeObserved with another settings reader.
func sendWelcomeObservedWith(reader appmail.Settings, observe func()) error {
	mailer := appmail.NewMailer(appmail.MailerDeps{
		Transport: observingMail{observe: observe},
		Config:    appfacades.Config(),
		Settings:  reader,
	})
	return mailer.To([]string{"nobody@example.test"}).Send(&mails.WelcomeMail{To: "nobody@example.test"})
}

// failedSMTPRead is the settings service with a mail_smtp read that fails.
type failedSMTPRead struct{ appmail.Settings }

func (failedSMTPRead) EffectiveMailSMTP(context.Context) (settings.MailSMTP, error) {
	return settings.MailSMTP{}, errMailReadFailed
}

// failedDeliveryRead is the settings service with a mail_delivery read that
// fails after returning a header.
type failedDeliveryRead struct{ appmail.Settings }

func (failedDeliveryRead) EffectiveMailDelivery(context.Context) (settings.MailDelivery, error) {
	return settings.MailDelivery{Address: "replaced@example.test", Name: "Replaced", UseAddress: true, UseName: true}, errMailReadFailed
}

// observingMail is the transport. Send runs observe and accepts the message.
type observingMail struct{ observe func() }

func (m observingMail) Attach([]string) contractsmail.Mail { return m }
func (m observingMail) Bcc([]string) contractsmail.Mail    { return m }
func (m observingMail) Cc([]string) contractsmail.Mail     { return m }
func (m observingMail) Content(contractsmail.Content) contractsmail.Mail {
	return m
}
func (m observingMail) From(contractsmail.Address) contractsmail.Mail { return m }
func (m observingMail) Headers(map[string]string) contractsmail.Mail  { return m }
func (m observingMail) Queue(...contractsmail.Mailable) error         { return nil }
func (m observingMail) Send(...contractsmail.Mailable) error {
	if m.observe != nil {
		m.observe()
	}
	return nil
}
func (m observingMail) Subject(string) contractsmail.Mail { return m }
func (m observingMail) To([]string) contractsmail.Mail    { return m }
