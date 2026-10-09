package facades

import (
	"errors"

	"github.com/goravel/framework/contracts/mail"
	goravelfacades "github.com/goravel/framework/facades"
)

var errMailerRequired = errors.New("mail: mailer is required")

// Mail returns the process mailer: app/providers/mail.Mailer, bound by
// MailServiceProvider. Each Send reads mail_smtp and the mail_delivery From
// header first. SMTP dials unless MAIL_MAILER is log; the log driver is
// refused in production. A mailer that cannot be resolved refuses every send.
func Mail() mail.Mail {
	resolved := goravelfacades.Mail()
	if resolved == nil {
		return unavailableMail{}
	}
	return resolved
}

type unavailableMail struct{}

func (unavailableMail) Attach([]string) mail.Mail           { return unavailableMail{} }
func (unavailableMail) Bcc([]string) mail.Mail              { return unavailableMail{} }
func (unavailableMail) Cc([]string) mail.Mail               { return unavailableMail{} }
func (unavailableMail) Content(mail.Content) mail.Mail      { return unavailableMail{} }
func (unavailableMail) From(mail.Address) mail.Mail         { return unavailableMail{} }
func (unavailableMail) Headers(map[string]string) mail.Mail { return unavailableMail{} }
func (unavailableMail) Queue(...mail.Mailable) error        { return errMailerRequired }
func (unavailableMail) Send(...mail.Mailable) error         { return errMailerRequired }
func (unavailableMail) Subject(string) mail.Mail            { return unavailableMail{} }
func (unavailableMail) To([]string) mail.Mail               { return unavailableMail{} }
