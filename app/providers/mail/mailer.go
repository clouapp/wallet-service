// Package mail is facades.Mail(): the framework SMTP mail, sent with the
// stored mail_smtp row and mail_delivery From header read at send time.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	contractsconfig "github.com/goravel/framework/contracts/config"
	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/config"
)

var (
	// errMailerRequired is returned when a send has no transport or no config.
	errMailerRequired = errors.New("mail: mailer is required")
	// errQueueRefused is returned by Queue. A queued mailable is a rendered
	// payload at rest, and credential mail must not sit in a queue.
	errQueueRefused = errors.New("mail: queue is refused")
)

const (
	smtpUnreadMessage = "mail smtp settings unread; keeping the env mailer"
	fromUnreadMessage = "mail delivery settings unread; keeping the env from header"
)

// sendLock serialises every send. The framework dials with the process
// config (mail.host, mail.port, mail.from.*), so a send publishes its
// settings there and puts the env document back before the next send reads
// it.
var sendLock sync.Mutex

// Settings reads the stored mail rows at send time. A false Use field keeps
// that env value, and a failed read keeps the whole env group. The driver
// stored on mail_delivery does not select a transport.
type Settings interface {
	EffectiveMailSMTP(ctx context.Context) (settings.MailSMTP, error)
	EffectiveMailDelivery(ctx context.Context) (settings.MailDelivery, error)
}

// Mailer implements the framework mail contract over the SMTP transport.
// Send overlays the settings on the env mail document, publishes it on the
// process config for the dial, and restores the env document afterwards.
// The log driver accepts the message without a dial and is refused in
// production before anything is published. Queue refuses.
type Mailer struct {
	transport contractsmail.Mail
	config    contractsconfig.Config
	settings  Settings
}

// MailerDeps is the framework SMTP application, the process config it dials
// with, and the settings reader. A nil Settings keeps the env document.
type MailerDeps struct {
	Transport contractsmail.Mail
	Config    contractsconfig.Config
	Settings  Settings
}

// NewMailer wires the mailer.
func NewMailer(deps MailerDeps) *Mailer {
	return &Mailer{transport: deps.Transport, config: deps.Config, settings: deps.Settings}
}

// Attach implements mail.Mail.
func (m *Mailer) Attach(files []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Attach(files))
}

// Bcc implements mail.Mail.
func (m *Mailer) Bcc(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Bcc(addresses))
}

// Cc implements mail.Mail.
func (m *Mailer) Cc(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Cc(addresses))
}

// Content implements mail.Mail.
func (m *Mailer) Content(content contractsmail.Content) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Content(content))
}

// From implements mail.Mail.
func (m *Mailer) From(address contractsmail.Address) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.From(address))
}

// Headers implements mail.Mail.
func (m *Mailer) Headers(headers map[string]string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Headers(headers))
}

// Subject implements mail.Mail.
func (m *Mailer) Subject(subject string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.Subject(subject))
}

// To implements mail.Mail.
func (m *Mailer) To(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	return m.with(m.transport.To(addresses))
}

// Queue refuses. facades.Mail().Queue() must not render a mailable into a
// payload that sits in a queue or a worker log. Callers send with Send.
func (m *Mailer) Queue(...contractsmail.Mailable) error {
	return errQueueRefused
}

// Send publishes this send's mail document, dials, and restores the env
// document, all under the send lock.
func (m *Mailer) Send(mailable ...contractsmail.Mailable) error {
	if m == nil || m.transport == nil || m.config == nil {
		return errMailerRequired
	}
	sendLock.Lock()
	defer sendLock.Unlock()

	env := cloneMap(m.config.Get("mail"))
	doc := m.resolve(context.Background(), env)
	useLog, err := logTransport(doc)
	if err != nil {
		return err
	}
	m.config.Add("mail", doc)
	defer m.config.Add("mail", env)
	if useLog {
		slog.Info("mail accepted by the log driver")
		return nil
	}
	return m.transport.Send(mailable...)
}

func (m *Mailer) with(next contractsmail.Mail) contractsmail.Mail {
	if next == nil {
		return m
	}
	return &Mailer{transport: next, config: m.config, settings: m.settings}
}

// resolve returns the mail document for one send: a copy of env with the
// stored settings overlaid. A missing reader or a failed read keeps the env
// values.
func (m *Mailer) resolve(ctx context.Context, env map[string]any) map[string]any {
	doc := cloneMap(env)
	if m.settings == nil {
		return doc
	}
	if dial, err := m.settings.EffectiveMailSMTP(ctx); err != nil {
		slog.Warn(smtpUnreadMessage)
	} else {
		mergeDial(doc, dial)
	}
	if from, err := m.settings.EffectiveMailDelivery(ctx); err != nil {
		slog.Warn(fromUnreadMessage)
	} else {
		mergeFrom(doc, from)
	}
	return doc
}

// logTransport reports whether the document selects the log driver, which
// this environment allows. The log driver is refused in production.
func logTransport(doc map[string]any) (bool, error) {
	driver, _ := doc["driver"].(string)
	appEnv, _ := doc["app_env"].(string)
	if err := config.RefuseLogDriver(appEnv, driver); err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(driver), "log"), nil
}

// mergeDial overlays a stored mail_smtp row on the env mail document.
// Goravel reads mail.host, mail.port, mail.username, and mail.password at
// send time and picks the socket from the port. encryption is written on
// the smtp mailer as well. A field that is not in use stays on the env copy.
func mergeDial(doc map[string]any, overlay settings.MailSMTP) {
	smtp := ensureSMTP(doc)
	if overlay.UseHost {
		smtp["host"] = overlay.Host
		doc["host"] = overlay.Host
	}
	if overlay.UsePort {
		smtp["port"] = overlay.Port
		doc["port"] = overlay.Port
	}
	if overlay.UseEncryption {
		smtp["encryption"] = overlay.Encryption
		doc["encryption"] = overlay.Encryption
	}
	if overlay.UseUsername {
		smtp["username"] = overlay.Username
		doc["username"] = overlay.Username
	}
	if overlay.UsePassword {
		smtp["password"] = overlay.Password
		doc["password"] = overlay.Password
	}
}

// mergeFrom overlays a stored mail_delivery From header on the env mail
// document. Goravel reads mail.from.address and mail.from.name at send time
// when the mailable leaves From empty. A field that is not in use stays on
// the env copy.
func mergeFrom(doc map[string]any, overlay settings.MailDelivery) {
	if !overlay.UseAddress && !overlay.UseName {
		return
	}
	from, _ := doc["from"].(map[string]any)
	if from == nil {
		from = map[string]any{}
		doc["from"] = from
	}
	if overlay.UseAddress {
		from["address"] = overlay.Address
	}
	if overlay.UseName {
		from["name"] = overlay.Name
	}
}

func ensureSMTP(doc map[string]any) map[string]any {
	mailers, _ := doc["mailers"].(map[string]any)
	if mailers == nil {
		mailers = map[string]any{}
		doc["mailers"] = mailers
	}
	smtp, _ := mailers["smtp"].(map[string]any)
	if smtp == nil {
		smtp = map[string]any{}
		mailers["smtp"] = smtp
	}
	return smtp
}

// cloneMap copies a config document deep enough that the overlay never
// writes into the env copy.
func cloneMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok || typed == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(typed))
	for key, item := range typed {
		if nested, ok := item.(map[string]any); ok {
			out[key] = cloneMap(nested)
			continue
		}
		out[key] = item
	}
	return out
}

var _ contractsmail.Mail = (*Mailer)(nil)
