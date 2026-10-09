package mail

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"testing"

	contractsconfig "github.com/goravel/framework/contracts/config"
	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/config"
)

func TestDeliver_Uses_SMTPAndFromWhenTheRowsExist(t *testing.T) {
	const stored = "stored-mailbox-secret"
	cfg := newDocumentConfig(map[string]any{
		"host": "env-host",
		"from": map[string]any{"address": "env@example.test", "name": "Env"},
		"mailers": map[string]any{
			"smtp": map[string]any{"transport": "smtp", "host": "env-host", "password": "env-mailbox-secret"},
		},
	})
	var dialed map[string]any
	transport := &recordingMail{onSend: func() { dialed = cfg.mail }}
	mailer := NewMailer(MailerDeps{Transport: transport, Config: cfg, Settings: storedSettings{
		smtp: settings.MailSMTP{
			Host: "127.0.0.1", Port: 2525, Password: stored,
			UseHost: true, UsePort: true, UsePassword: true,
		},
		from: settings.MailDelivery{Address: "from-header@example.test", Name: "Macro", UseAddress: true, UseName: true},
	}})
	if err := mailer.To([]string{"nobody@example.test"}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !transport.sent || dialed == nil {
		t.Fatal("the send did not go through the mailer")
	}
	if dialed["host"] != "127.0.0.1" {
		t.Fatal("the send did not use mail_smtp")
	}
	from := dialed["from"].(map[string]any)
	if from["address"] != "from-header@example.test" || from["name"] != "Macro" {
		t.Fatal("the send did not use mail_delivery")
	}
	smtp := dialed["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" {
		t.Fatal("the send left SMTP")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(stored)) != 1 {
		t.Fatal("the send did not use the mail_smtp password")
	}
	if cfg.mail["host"] != "env-host" || cfg.mail["from"].(map[string]any)["address"] != "env@example.test" {
		t.Fatal("the env document was not restored after the send")
	}
}

func TestDeliver_Keeps_TheEnvDocumentWhenTheRowsAreMissing(t *testing.T) {
	const envPassword = "env-mailbox-secret"
	cfg := newDocumentConfig(map[string]any{
		"from": map[string]any{"address": "env@example.test", "name": "Env"},
		"mailers": map[string]any{
			"smtp": map[string]any{"transport": "smtp", "host": "env-host", "password": envPassword},
		},
	})
	var dialed map[string]any
	transport := &recordingMail{onSend: func() { dialed = cfg.mail }}
	if err := NewMailer(MailerDeps{Transport: transport, Config: cfg, Settings: storedSettings{}}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !transport.sent {
		t.Fatal("a missing row skipped the send")
	}
	smtp := dialed["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" || smtp["host"] != "env-host" {
		t.Fatal("a missing row replaced the env mailer")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(envPassword)) != 1 {
		t.Fatal("a missing row replaced the env password")
	}
	from := dialed["from"].(map[string]any)
	if from["address"] != "env@example.test" || from["name"] != "Env" {
		t.Fatal("a missing row replaced the env from header")
	}
}

func TestDeliver_Keeps_TheEnvDocumentWhenTheReadFails(t *testing.T) {
	cfg := newDocumentConfig(map[string]any{
		"mailers": map[string]any{"smtp": map[string]any{"host": "env-host"}},
		"from":    map[string]any{"address": "env@example.test", "name": "Env"},
	})
	var dialed map[string]any
	transport := &recordingMail{onSend: func() { dialed = cfg.mail }}
	failing := storedSettings{
		smtp:    settings.MailSMTP{Host: "127.0.0.1", Password: "stored-mailbox-secret", UseHost: true, UsePassword: true},
		smtpErr: errors.New("db down"),
		from:    settings.MailDelivery{Address: "replaced@example.test", Name: "Replaced", UseAddress: true, UseName: true},
		fromErr: errors.New("db down"),
	}
	if err := NewMailer(MailerDeps{Transport: transport, Config: cfg, Settings: failing}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	smtp := dialed["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["host"] != "env-host" || smtp["password"] != nil {
		t.Fatal("a failed read replaced the env mailer")
	}
	from := dialed["from"].(map[string]any)
	if from["address"] != "env@example.test" || from["name"] != "Env" {
		t.Fatal("a failed read replaced the env from header")
	}
}

func TestDeliver_Restores_TheEnvDocumentWhenTheDialFails(t *testing.T) {
	cfg := newDocumentConfig(map[string]any{"host": "env-host"})
	dialErr := errors.New("connection refused")
	transport := &recordingMail{err: dialErr}
	mailer := NewMailer(MailerDeps{Transport: transport, Config: cfg, Settings: storedSettings{
		smtp: settings.MailSMTP{Host: "127.0.0.1", UseHost: true},
	}})
	if err := mailer.Send(); !errors.Is(err, dialErr) {
		t.Fatalf("send error = %v, want the dial error", err)
	}
	if cfg.mail["host"] != "env-host" {
		t.Fatal("a failed dial left the send's document on the process config")
	}
}

// The framework dials with the process config, so the lock is held from the
// publish to the restore: no other send can publish its document in between.
func TestDeliver_Holds_TheSendLockAcrossTheDial(t *testing.T) {
	cfg := newDocumentConfig(map[string]any{})
	var heldDuringDial bool
	transport := &recordingMail{onSend: func() { heldDuringDial = !sendLock.TryLock() }}
	if err := NewMailer(MailerDeps{Transport: transport, Config: cfg}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !heldDuringDial {
		sendLock.Unlock()
		t.Fatal("another send could publish its document during this dial")
	}
	if !sendLock.TryLock() {
		t.Fatal("the send kept the lock after it returned")
	}
	sendLock.Unlock()
}

func TestQueue_Refuses_WithoutPublishingOrDialing(t *testing.T) {
	cfg := newDocumentConfig(map[string]any{})
	transport := &recordingMail{}
	err := NewMailer(MailerDeps{Transport: transport, Config: cfg}).Queue()
	if !errors.Is(err, errQueueRefused) {
		t.Fatalf("queue error = %v", err)
	}
	if transport.queued || len(cfg.writes) != 0 {
		t.Fatal("queue published the mail document or reached the transport")
	}
	if err := (*Mailer)(nil).Queue(); !errors.Is(err, errQueueRefused) {
		t.Fatalf("nil mailer queue error = %v", err)
	}
}

func TestNew_Mailer_KeepsItsDependencies(t *testing.T) {
	cfg := newDocumentConfig(map[string]any{})
	transport := &recordingMail{}
	reader := storedSettings{}
	got := NewMailer(MailerDeps{Transport: transport, Config: cfg, Settings: reader})
	if got == nil || got.transport != transport || got.config != cfg || got.settings != reader {
		t.Fatal("the mailer dropped a dependency")
	}
	empty := NewMailer(MailerDeps{})
	if empty == nil || empty.transport != nil || empty.config != nil || empty.settings != nil {
		t.Fatal("a missing dependency was filled in")
	}
	chained, ok := got.To([]string{"nobody@example.test"}).(*Mailer)
	if !ok || chained.config != cfg || chained.settings != reader {
		t.Fatal("a builder call dropped the config or the settings")
	}
}

func TestDeliver_Accepts_TheLogDriverWithoutDialing(t *testing.T) {
	const secret = "env-mailbox-secret"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg := newDocumentConfig(map[string]any{
		"driver":   "log",
		"app_env":  "local",
		"password": secret,
		"mailers":  map[string]any{"smtp": map[string]any{"password": secret}},
	})
	transport := &recordingMail{}
	err := NewMailer(MailerDeps{Transport: transport, Config: cfg}).Send()
	if err != nil {
		t.Fatal("log driver send failed")
	}
	if transport.sent {
		t.Fatal("log driver dialed")
	}
	if len(cfg.writes) != 2 {
		t.Fatal("log driver skipped the mail document")
	}
	text := logs.String()
	if strings.Contains(text, secret) {
		t.Fatal("log driver wrote a credential")
	}
	if !strings.Contains(text, "mail accepted by the log driver") {
		t.Fatal("log driver did not accept the message")
	}
}

func TestDeliver_Refuses_TheLogDriverInProduction(t *testing.T) {
	const secret = "env-mailbox-secret"
	cfg := newDocumentConfig(map[string]any{
		"driver":   "log",
		"app_env":  "production",
		"password": secret,
	})
	transport := &recordingMail{}
	err := NewMailer(MailerDeps{Transport: transport, Config: cfg}).Send()
	if !errors.Is(err, config.ErrLogDriverRefused) {
		t.Fatal("production log driver was not refused")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("refusal included a credential")
	}
	if transport.sent || len(cfg.writes) != 0 {
		t.Fatal("refused log driver dialed or published")
	}
}

func TestDeliver_Refuses_AMissingMailer(t *testing.T) {
	if err := NewMailer(MailerDeps{Config: newDocumentConfig(nil)}).Send(); !errors.Is(err, errMailerRequired) {
		t.Fatal("a mailer without a transport was sent")
	}
	if err := NewMailer(MailerDeps{Transport: &recordingMail{}}).Send(); !errors.Is(err, errMailerRequired) {
		t.Fatal("a mailer without a config was sent")
	}
	if err := (*Mailer)(nil).Send(); !errors.Is(err, errMailerRequired) {
		t.Fatal("a nil mailer was sent")
	}
}

// documentConfig is the process config as the mailer sees it: the "mail"
// document, and every document written to it.
type documentConfig struct {
	contractsconfig.Config
	mail   map[string]any
	writes []map[string]any
}

func newDocumentConfig(mail map[string]any) *documentConfig {
	return &documentConfig{mail: mail}
}

func (c *documentConfig) Get(path string, _ ...any) any {
	if path != "mail" {
		return nil
	}
	return c.mail
}

func (c *documentConfig) Add(name string, value any) {
	if name != "mail" {
		return
	}
	doc, _ := value.(map[string]any)
	c.mail = doc
	c.writes = append(c.writes, doc)
}

// storedSettings answers the two settings reads with fixed rows or errors.
type storedSettings struct {
	smtp    settings.MailSMTP
	smtpErr error
	from    settings.MailDelivery
	fromErr error
}

func (s storedSettings) EffectiveMailSMTP(context.Context) (settings.MailSMTP, error) {
	return s.smtp, s.smtpErr
}

func (s storedSettings) EffectiveMailDelivery(context.Context) (settings.MailDelivery, error) {
	return s.from, s.fromErr
}

// recordingMail is the SMTP transport. onSend runs where the dial would, so a
// test sees the document the dial reads.
type recordingMail struct {
	sent   bool
	queued bool
	err    error
	onSend func()
}

func (m *recordingMail) Attach([]string) contractsmail.Mail { return m }
func (m *recordingMail) Bcc([]string) contractsmail.Mail    { return m }
func (m *recordingMail) Cc([]string) contractsmail.Mail     { return m }
func (m *recordingMail) Content(contractsmail.Content) contractsmail.Mail {
	return m
}
func (m *recordingMail) From(contractsmail.Address) contractsmail.Mail { return m }
func (m *recordingMail) Headers(map[string]string) contractsmail.Mail  { return m }
func (m *recordingMail) Queue(...contractsmail.Mailable) error {
	m.queued = true
	return nil
}
func (m *recordingMail) Send(...contractsmail.Mailable) error {
	m.sent = true
	if m.onSend != nil {
		m.onSend()
	}
	return m.err
}
func (m *recordingMail) Subject(string) contractsmail.Mail { return m }
func (m *recordingMail) To([]string) contractsmail.Mail    { return m }
