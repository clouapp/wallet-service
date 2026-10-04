package facades

import (
	"context"
	"errors"
	"log/slog"
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

var (
	mailMu           sync.Mutex
	readMailSMTP     MailDialReader
	readMailFrom     MailFromReader
	mailSendObserver func()
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

// RestoreMailBaseline puts the env mail config back. A send already does
// this when it returns.
func RestoreMailBaseline() {
	mailMu.Lock()
	defer mailMu.Unlock()
	restoreMailBaseline()
}

// Mail returns the process mailer. Each Send reads mail_smtp and the
// mail_delivery From header first.
func Mail() mail.Mail {
	return smtpMail{inner: goravelfacades.Mail()}
}

type smtpMail struct {
	inner mail.Mail
}

func (m smtpMail) Attach(files []string) mail.Mail {
	return smtpMail{inner: m.inner.Attach(files)}
}

func (m smtpMail) Bcc(addresses []string) mail.Mail {
	return smtpMail{inner: m.inner.Bcc(addresses)}
}

func (m smtpMail) Cc(addresses []string) mail.Mail {
	return smtpMail{inner: m.inner.Cc(addresses)}
}

func (m smtpMail) Content(content mail.Content) mail.Mail {
	return smtpMail{inner: m.inner.Content(content)}
}

func (m smtpMail) From(address mail.Address) mail.Mail {
	return smtpMail{inner: m.inner.From(address)}
}

func (m smtpMail) Headers(headers map[string]string) mail.Mail {
	return smtpMail{inner: m.inner.Headers(headers)}
}

func (m smtpMail) Queue(mailable ...mail.Mailable) error {
	return m.withSMTP(func() error { return m.inner.Queue(mailable...) })
}

func (m smtpMail) Send(mailable ...mail.Mailable) error {
	return m.withSMTP(func() error { return m.inner.Send(mailable...) })
}

func (m smtpMail) Subject(subject string) mail.Mail {
	return smtpMail{inner: m.inner.Subject(subject)}
}

func (m smtpMail) To(addresses []string) mail.Mail {
	return smtpMail{inner: m.inner.To(addresses)}
}

func (m smtpMail) withSMTP(send func() error) error {
	if m.inner == nil {
		return errMailerRequired
	}
	mailMu.Lock()
	defer mailMu.Unlock()
	applyMailDial(context.Background())
	defer restoreMailBaseline()
	if mailSendObserver != nil {
		mailSendObserver()
	}
	return send()
}

func applyMailDial(ctx context.Context) {
	base := cloneMailBaseline()
	applySMTPOverlay(ctx, base)
	applyFromOverlay(ctx, base)
	writeMail(base)
}

func applySMTPOverlay(ctx context.Context, base map[string]any) {
	reader := readMailSMTP
	if reader == nil {
		return
	}
	overlay, err := reader(ctx)
	if err != nil {
		slog.Warn("mail smtp settings unread; keeping the env mailer")
		return
	}
	mergeMailDial(base, overlay)
}

func applyFromOverlay(ctx context.Context, base map[string]any) {
	reader := readMailFrom
	if reader == nil {
		return
	}
	overlay, err := reader(ctx)
	if err != nil {
		slog.Warn("mail delivery settings unread; keeping the env from header")
		return
	}
	mergeMailFrom(base, overlay)
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

// mergeMailDial overlays a stored mail_smtp row on the env mail document.
// Goravel reads mail.host, mail.port, mail.username, and mail.password at
// send time and picks the socket from the port. encryption is written on
// the smtp mailer as well. A field that is not in use stays on the env copy.
func mergeMailDial(cfg map[string]any, overlay MailDial) {
	if cfg == nil {
		return
	}
	smtp := ensureSMTP(cfg)
	if overlay.UseHost {
		smtp["host"] = overlay.Host
		cfg["host"] = overlay.Host
	}
	if overlay.UsePort {
		smtp["port"] = overlay.Port
		cfg["port"] = overlay.Port
	}
	if overlay.UseEncryption {
		smtp["encryption"] = overlay.Encryption
		cfg["encryption"] = overlay.Encryption
	}
	if overlay.UseUsername {
		smtp["username"] = overlay.Username
		cfg["username"] = overlay.Username
	}
	if overlay.UsePassword {
		smtp["password"] = overlay.Password
		cfg["password"] = overlay.Password
	}
}

// mergeMailFrom overlays a stored mail_delivery From header on the env mail
// document. Goravel reads mail.from.address and mail.from.name at send time
// when the mailable leaves From empty. A field that is not in use stays on
// the env copy.
func mergeMailFrom(cfg map[string]any, overlay MailFrom) {
	if cfg == nil || (!overlay.UseAddress && !overlay.UseName) {
		return
	}
	from, _ := cfg["from"].(map[string]any)
	if from == nil {
		from = map[string]any{}
		cfg["from"] = from
	}
	if overlay.UseAddress {
		from["address"] = overlay.Address
	}
	if overlay.UseName {
		from["name"] = overlay.Name
	}
}

func ensureSMTP(cfg map[string]any) map[string]any {
	mailers, _ := cfg["mailers"].(map[string]any)
	if mailers == nil {
		mailers = map[string]any{}
		cfg["mailers"] = mailers
	}
	smtp, _ := mailers["smtp"].(map[string]any)
	if smtp == nil {
		smtp = map[string]any{}
		mailers["smtp"] = smtp
	}
	return smtp
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
