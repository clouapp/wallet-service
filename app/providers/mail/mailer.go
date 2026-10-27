// Package mail is facades.Mail(): the transport mail_delivery.driver (else
// MAIL_MAILER) selects at send time: the framework SMTP mail with the stored
// mail_smtp row, or the HTTP API of mailgun, resend or postmark with their
// groups, under the mail_delivery From header.
package mail

import (
	"context"
	"errors"
	"fmt"
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
// stored on mail_delivery selects the transport.
type Settings interface {
	EffectiveMailSMTP(ctx context.Context) (settings.MailSMTP, error)
	EffectiveMailDelivery(ctx context.Context) (settings.MailDelivery, error)
	EffectiveMailProvider(ctx context.Context, driver string) (settings.MailProvider, error)
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
	api       *apiTransport
	// msg mirrors the builder calls for a transport the framework does not run.
	msg message
}

// MailerDeps is the framework SMTP application, the process config it dials
// with, and the settings reader. A nil Settings keeps the env document.
// APIBases points the HTTP providers elsewhere (tests); zero is their public API.
type MailerDeps struct {
	Transport contractsmail.Mail
	Config    contractsconfig.Config
	Settings  Settings
	APIBases  APIBases
}

// NewMailer wires the mailer.
func NewMailer(deps MailerDeps) *Mailer {
	return &Mailer{transport: deps.Transport, config: deps.Config, settings: deps.Settings, api: newAPITransport(deps.APIBases),
		msg: message{headers: map[string]string{}}}
}

// Attach implements mail.Mail.
func (m *Mailer) Attach(files []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.attachments = append(next.attachments, files...)
	return m.with(m.transport.Attach(files), next)
}

// Bcc implements mail.Mail.
func (m *Mailer) Bcc(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.bcc = append([]string(nil), addresses...)
	return m.with(m.transport.Bcc(addresses), next)
}

// Cc implements mail.Mail.
func (m *Mailer) Cc(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.cc = append([]string(nil), addresses...)
	return m.with(m.transport.Cc(addresses), next)
}

// Content implements mail.Mail.
func (m *Mailer) Content(content contractsmail.Content) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.html, next.text = content.Html, content.Text
	return m.with(m.transport.Content(content), next)
}

// From implements mail.Mail.
func (m *Mailer) From(address contractsmail.Address) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.from = address
	return m.with(m.transport.From(address), next)
}

// Headers implements mail.Mail.
func (m *Mailer) Headers(headers map[string]string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	for key, value := range headers {
		next.headers[key] = value
	}
	return m.with(m.transport.Headers(headers), next)
}

// Subject implements mail.Mail.
func (m *Mailer) Subject(subject string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.subject = subject
	return m.with(m.transport.Subject(subject), next)
}

// To implements mail.Mail.
func (m *Mailer) To(addresses []string) contractsmail.Mail {
	if m == nil || m.transport == nil {
		return m
	}
	next := m.msg.clone()
	next.to = append([]string(nil), addresses...)
	return m.with(m.transport.To(addresses), next)
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

	ctx := context.Background()
	env := cloneMap(m.config.Get("mail"))
	doc := m.resolve(ctx, env)
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
	switch driver := documentDriver(doc); driver {
	case driverMailgun, driverResend, driverPostmark:
		return m.sendAPI(ctx, driver, doc, mailable)
	case driverSES:
		return errors.New("mail: the ses transport is not available yet")
	}
	return m.transport.Send(mailable...)
}

// sendAPI posts each mailable (or the builder's message when there is none)
// through the HTTP API of driver. A From address none of them set is the
// provider's override, else the mail_delivery (else MAIL_FROM_*) one. A missing
// credential fails the send: it never falls back to another transport.
func (m *Mailer) sendAPI(ctx context.Context, driver string, doc map[string]any, mailables []contractsmail.Mailable) error {
	if m.settings == nil || m.api == nil {
		return errMissingCredential
	}
	provider, err := m.settings.EffectiveMailProvider(ctx, driver)
	if err != nil {
		return fmt.Errorf("mail: %s settings unread", driver)
	}
	if len(mailables) == 0 {
		mailables = []contractsmail.Mailable{nil}
	}
	for _, mailable := range mailables {
		msg, err := m.msg.withMailable(mailable)
		if err != nil {
			return err
		}
		if msg.from.Address == "" {
			msg.from = senderOf(provider, doc)
		}
		if err := m.api.send(ctx, provider, msg); err != nil {
			return err
		}
	}
	slog.Info("mail sent through the provider API", "driver", driver)
	return nil
}

// senderOf is the From of a message that set none: the provider override, else
// the document's mail.from.
func senderOf(provider settings.MailProvider, doc map[string]any) contractsmail.Address {
	from, _ := doc["from"].(map[string]any)
	address, _ := from["address"].(string)
	name, _ := from["name"].(string)
	if provider.FromAddress != "" {
		address = provider.FromAddress
	}
	if provider.FromName != "" {
		name = provider.FromName
	}
	return contractsmail.Address{Address: address, Name: name}
}

// documentDriver is the transport the document selects, smtp when it names none.
func documentDriver(doc map[string]any) string {
	driver, _ := doc["driver"].(string)
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		return driverSMTP
	}
	return driver
}

func (m *Mailer) with(next contractsmail.Mail, msg message) contractsmail.Mail {
	if next == nil {
		return m
	}
	return &Mailer{transport: next, config: m.config, settings: m.settings, api: m.api, msg: msg}
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
		if from.UseDriver {
			doc["driver"] = from.Driver
		}
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
