package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	contractsmail "github.com/goravel/framework/contracts/mail"
	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/mailer"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/mails"
)

// sendTimeout bounds one delivery.
//
// Without it a hung provider holds the worker forever, and config/queue.go runs
// a single concurrent job -- one stuck send would stop every email in the
// system, not just its own.
const sendTimeout = 10 * time.Second

// Mailer is the application's send path. It is the real thing; the Facade that
// answers facades.Mail() is an adapter over it.
type Mailer struct {
	renderer   contractsmail.Template
	config     *mailer.Config
	factory    *mailer.Factory
	resolveJob func() (contractsqueue.Job, error)
}

// NewMailer wires the mailer.
//
// The job is resolved lazily rather than held: the container owns its identity,
// and two instances would mean the worker registered one while the queue
// referenced another.
func NewMailer(
	renderer contractsmail.Template,
	config *mailer.Config,
	factory *mailer.Factory,
	resolveJob func() (contractsqueue.Job, error),
) *Mailer {
	return &Mailer{renderer: renderer, config: config, factory: factory, resolveJob: resolveJob}
}

// Render exposes the template engine to the facade, which may be handed a view
// with no mailable around it.
func (m *Mailer) Render(view string, with map[string]any) (string, error) {
	return m.renderer.Render(view, with)
}

// Queue renders a mailable and hands it to the queue. It is what
// facades.Mail().Queue(mailable) reaches.
func (m *Mailer) Queue(mailable contractsmail.Mailable) error {
	msg, err := messageFrom(m.renderer, mailable)
	if err != nil {
		return err
	}
	return m.QueueMessage(msg)
}

// QueueMessage enqueues an already-rendered message.
func (m *Mailer) QueueMessage(msg mailer.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mail: marshal message: %w", err)
	}
	if m.resolveJob == nil {
		return fmt.Errorf("mail: no send job wired; cannot queue")
	}
	job, err := m.resolveJob()
	if err != nil {
		return fmt.Errorf("mail: resolve send job: %w", err)
	}
	return facades.Queue().Job(job, []contractsqueue.Arg{
		{Value: string(payload), Type: "string"},
	}).Dispatch()
}

// Send renders and delivers synchronously. driver may be empty for the active
// one; naming it is what the admin test endpoint does, and the only reason this
// is not simply Queue.
func (m *Mailer) Send(ctx context.Context, mailable contractsmail.Mailable, driver string) error {
	msg, err := messageFrom(m.renderer, mailable)
	if err != nil {
		return err
	}
	return m.SendMessage(ctx, msg, driver)
}

// SendTest delivers a test message to `to` through driver, or through the
// active one when driver is empty, and returns the driver that carried it.
// Synchronous on purpose: the provider's answer is what the operator is asking
// for, and a *DeliveryError carries it.
func (m *Mailer) SendTest(ctx context.Context, to, driver string) (string, error) {
	if driver == "" {
		active, err := m.config.Driver()
		if err != nil {
			return "", fmt.Errorf("mail: read the active driver: %w", err)
		}
		driver = active
	}
	if !slices.Contains(mailer.AllDrivers(), driver) {
		return "", fmt.Errorf("mail: driver %q: %w", driver, mailer.ErrUnknownDriver)
	}
	if err := m.Send(ctx, mails.NewTestMail(to, driver), driver); err != nil {
		return "", err
	}
	return driver, nil
}

// SendMessage delivers an already-rendered message synchronously.
func (m *Mailer) SendMessage(ctx context.Context, msg mailer.Message, driver string) error {
	return Deliver(ctx, m.config, m.factory, msg, driver)
}

// Deliver is the one place a driver is resolved and a transport is called.
//
// Both the queue worker (jobs.SendMailJob) and the synchronous path go through
// it, so the two cannot drift on which driver they pick or which sender they
// stamp.
func Deliver(
	ctx context.Context,
	config *mailer.Config,
	factory *mailer.Factory,
	msg mailer.Message,
	driver string,
) error {
	if driver == "" {
		active, err := config.Driver()
		if err != nil {
			return err
		}
		driver = active
	}
	// The settings are a default, not an override: a mailable that named its own
	// sender keeps it.
	if msg.From.Address == "" {
		from, err := config.From(driver)
		if err != nil {
			return err
		}
		msg.From = from
	}
	transport, err := factory.Get(driver)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := transport.Send(ctx, msg); err != nil {
		return &DeliveryError{Err: err}
	}
	return nil
}

// DeliveryError is a transport's failure to deliver, worded by the provider:
// "domain not verified", "api_key is not set". It is the one mail error an
// operator can act on, as opposed to a settings read or a template that failed.
type DeliveryError struct {
	Err error
}

// Error is not labelled with the driver: every transport already labels its
// own errors with it, and doubling the name ("mail: resend: resend: 401") is
// what an operator would read in the admin when a key is wrong.
func (e *DeliveryError) Error() string { return "mail: " + e.Err.Error() }

func (e *DeliveryError) Unwrap() error { return e.Err }
