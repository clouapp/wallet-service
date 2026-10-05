package mail

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/app/providers/mailer"
)

// errMailerRequired is returned when a send has no mailer or no transport.
var errMailerRequired = errors.New("mail: mailer is required")

// errQueueRefused is returned by Facade.Queue. A queued mailable is a
// rendered payload at rest, and credential mail must not sit in a queue.
var errQueueRefused = errors.New("mail: queue is refused")

// Mailer publishes mailer.Config onto the process mail document and then
// dials. The transport stays SMTP.
type Mailer struct {
	config *mailer.Config
	gate   func(func() error) error
}

// MailerDeps is the settings reader and the process mail lock.
// Either field may be nil. A nil Gate still sends.
type MailerDeps struct {
	Config *mailer.Config
	Gate   func(func() error) error
}

// NewMailer wires the mailer. Gate holds the process mail lock for the send.
// A nil Gate still sends.
func NewMailer(deps MailerDeps) *Mailer {
	return &Mailer{config: deps.Config, gate: deps.Gate}
}

// Config returns the settings reader this mailer uses at send time.
func (m *Mailer) Config() *mailer.Config {
	if m == nil {
		return nil
	}
	return m.config
}

// Deliver reads mailer.Config, publishes it for the SMTP dial, runs the
// observer, calls send, and restores the env document.
func (m *Mailer) Deliver(send func() error) error {
	if m == nil || m.config == nil || send == nil {
		return errMailerRequired
	}
	run := func() error {
		m.config.WriteResolved(context.Background())
		defer m.config.Restore()
		m.config.Observe()
		return send()
	}
	if m.gate == nil {
		return run()
	}
	return m.gate(run)
}
