package facades

import (
	"context"
	"errors"
	"sync"

	"github.com/goravel/framework/contracts/mail"
	goravelfacades "github.com/goravel/framework/facades"
)

var errMailerRequired = errors.New("mail: mailer is required")

// MailDial is one SMTP dial the process mailer may use for a single send.
// A false Use field leaves that env value in place. Password is plaintext
// for the dial only; it is not logged.
type MailDial struct {
	Host          string
	Port          int
	Encryption    string
	Username      string
	Password      string
	UseHost       bool
	UsePort       bool
	UseEncryption bool
	UseUsername   bool
	UsePassword   bool
}

// MailDialReader reads mail_smtp at send time. A non-nil error means the
// read failed and the env mailer stays.
type MailDialReader func(ctx context.Context) (MailDial, error)

// MailFrom is the From header one send may use. A false Use field leaves
// that env value in place. Address and name are not secrets.
type MailFrom struct {
	Address    string
	Name       string
	UseAddress bool
	UseName    bool
}

// MailFromReader reads mail_delivery at send time. A non-nil error means the
// read failed and the env From header stays.
type MailFromReader func(ctx context.Context) (MailFrom, error)

// MailSender replaces the SMTP dial for one send. The mailer still reads
// mail_smtp and mail_delivery before the sender runs. Nil restores the dial.
// The sender runs while the mail lock is held and must not send mail or call
// back into this package.
type MailSender func(mailable ...mail.Mailable) error

var (
	mailMu           sync.Mutex
	readMailSMTP     MailDialReader
	readMailFrom     MailFromReader
	mailSendObserver func()
	mailSender       MailSender
	mailBaselineOnce sync.Once
	mailBaseline     map[string]any
	mailBaselineSet  bool
)

// SetMailSMTPReader installs the per-send reader. It returns the previous
// reader so a test can put it back. Nil keeps the env mailer.
func SetMailSMTPReader(reader MailDialReader) MailDialReader {
	mailMu.Lock()
	defer mailMu.Unlock()
	previous := readMailSMTP
	readMailSMTP = reader
	return previous
}

// SetMailFromReader installs the per-send From reader. It returns the
// previous reader so a test can put it back. Nil keeps the env From header.
func SetMailFromReader(reader MailFromReader) MailFromReader {
	mailMu.Lock()
	defer mailMu.Unlock()
	previous := readMailFrom
	readMailFrom = reader
	return previous
}

// SetMailSendObserver runs after the per-send settings are applied and before
// the dial. The observer must not send mail. Nil clears it.
func SetMailSendObserver(observer func()) {
	mailMu.Lock()
	mailSendObserver = observer
	mailMu.Unlock()
}

// SetMailSender installs a dial replacement. It returns the previous sender
// so a test can put it back. Nil restores the SMTP dial.
func SetMailSender(sender MailSender) MailSender {
	mailMu.Lock()
	defer mailMu.Unlock()
	previous := mailSender
	mailSender = sender
	return previous
}

// MailSenderFunc returns the installed dial replacement. Nil means SMTP.
func MailSenderFunc() MailSender {
	mailMu.Lock()
	defer mailMu.Unlock()
	return mailSender
}

// RestoreMailBaseline puts the env mail config back. A send already does
// this when it returns.
func RestoreMailBaseline() {
	mailMu.Lock()
	defer mailMu.Unlock()
	restoreMailBaseline()
}

// Mail returns the process mailer. After boot it is the mail facade over the
// mailer and mailer.Config. Each Send reads mail_smtp and the mail_delivery
// From header first. SMTP dials unless MAIL_MAILER is log. The log driver
// is refused in production.
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

// GateMailSend holds the mail lock for one send. The mailer calls it so the
// readers, the published document, and the dial stay on the same snapshot.
func GateMailSend(send func() error) error {
	if send == nil {
		return errMailerRequired
	}
	mailMu.Lock()
	defer mailMu.Unlock()
	return send()
}

// ReadMailDial reads the installed mail_smtp reader. The caller holds the
// mail lock. installed is false when no reader is set.
func ReadMailDial(ctx context.Context) (MailDial, bool, error) {
	if readMailSMTP == nil {
		return MailDial{}, false, nil
	}
	dial, err := readMailSMTP(ctx)
	if err != nil {
		return MailDial{}, true, err
	}
	return dial, true, nil
}

// ReadMailFrom reads the installed mail_delivery From reader. The caller
// holds the mail lock. installed is false when no reader is set.
func ReadMailFrom(ctx context.Context) (MailFrom, bool, error) {
	if readMailFrom == nil {
		return MailFrom{}, false, nil
	}
	from, err := readMailFrom(ctx)
	if err != nil {
		return MailFrom{}, true, err
	}
	return from, true, nil
}

// MailBaseline returns a copy of the env mail document captured on the first
// send. The caller holds the mail lock.
func MailBaseline() map[string]any {
	return cloneMailBaseline()
}

// WriteMailConfig publishes one send's mail document. The caller holds the
// mail lock.
func WriteMailConfig(cfg map[string]any) {
	writeMail(cfg)
}

// RestoreMailDocument puts the env mail document back without taking the
// mail lock. GateMailSend already holds it.
func RestoreMailDocument() {
	restoreMailBaseline()
}

// ObserveMailSend runs the send observer. The caller holds the mail lock.
func ObserveMailSend() {
	if mailSendObserver != nil {
		mailSendObserver()
	}
}

func restoreMailBaseline() {
	if !mailBaselineSet {
		return
	}
	writeMail(cloneMailBaseline())
}

func cloneMailBaseline() map[string]any {
	mailBaselineOnce.Do(func() {
		mailBaseline = cloneAnyMap(Config().Get("mail"))
		mailBaselineSet = true
	})
	return cloneAnyMap(mailBaseline)
}

func writeMail(cfg map[string]any) {
	if cfg == nil {
		cfg = map[string]any{}
	}
	Config().Add("mail", cfg)
}

func cloneAnyMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok || typed == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(typed))
	for key, item := range typed {
		out[key] = cloneAny(item)
	}
	return out
}

func cloneAny(value any) any {
	nested, ok := value.(map[string]any)
	if !ok {
		return value
	}
	return cloneAnyMap(nested)
}
