package mail

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/app/providers/mailer"
)

// errMailerRequired is returned when a send has no mailer or no transport.
var errMailerRequired = errors.New("mail: mailer is required")

// Mailer publishes mailer.Config onto the process mail document and then
// dials. The transport stays SMTP.
type Mailer struct {
	config *mailer.Config
	gate   func(func() error) error
}

// NewMailer wires the mailer. gate holds the process mail lock for the send.
// A nil gate still sends.
func NewMailer(config *mailer.Config, gate func(func() error) error) *Mailer {
	return &Mailer{config: config, gate: gate}
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
