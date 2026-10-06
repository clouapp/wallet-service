package mail

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"testing"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/providers/mailer"
	"github.com/macrowallets/waas/config"
)

func TestDeliverUsesSMTPAndFromWhenTheRowsExist(t *testing.T) {
	const stored = "stored-mailbox-secret"
	var written map[string]any
	var restored bool
	var observed bool
	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"host": "env-host",
				"from": map[string]any{"address": "env@example.test", "name": "Env"},
				"mailers": map[string]any{
					"smtp": map[string]any{"transport": "smtp", "host": "env-host", "password": "env-mailbox-secret"},
				},
			}
		},
		Write:   func(doc map[string]any) { written = doc },
		Restore: func() { restored = true },
		Observe: func() { observed = true },
		SMTP: func(context.Context) (mailer.Dial, bool, error) {
			return mailer.Dial{
				Host: "127.0.0.1", Port: 2525, Password: stored,
				UseHost: true, UsePort: true, UsePassword: true,
			}, true, nil
		},
		From: func(context.Context) (mailer.From, bool, error) {
			return mailer.From{Address: "from-header@example.test", Name: "Macro", UseAddress: true, UseName: true}, true, nil
		},
	})
	transport := &recordingMail{}
	facade := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: transport})
	if err := facade.To([]string{"nobody@example.test"}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !transport.sent || !restored || !observed {
		t.Fatal("the send did not go through the mailer")
	}
	if written["host"] != "127.0.0.1" {
		t.Fatal("the send did not use mail_smtp")
	}
	from := written["from"].(map[string]any)
	if from["address"] != "from-header@example.test" || from["name"] != "Macro" {
		t.Fatal("the send did not use mail_delivery")
	}
	smtp := written["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" {
		t.Fatal("the send left SMTP")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(stored)) != 1 {
		t.Fatal("the send did not use the mail_smtp password")
	}
}

func TestDeliverKeepsTheEnvDocumentWhenTheRowsAreMissing(t *testing.T) {
	const envPassword = "env-mailbox-secret"
	var written map[string]any
	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"from": map[string]any{"address": "env@example.test", "name": "Env"},
				"mailers": map[string]any{
					"smtp": map[string]any{"transport": "smtp", "host": "env-host", "password": envPassword},
				},
			}
		},
		Write: func(doc map[string]any) { written = doc },
		SMTP: func(context.Context) (mailer.Dial, bool, error) {
			return mailer.Dial{}, false, nil
		},
		From: func(context.Context) (mailer.From, bool, error) {
			return mailer.From{}, false, nil
		},
	})
	transport := &recordingMail{}
	if err := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: transport}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !transport.sent {
		t.Fatal("a missing row skipped the send")
	}
	smtp := written["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" || smtp["host"] != "env-host" {
		t.Fatal("a missing row replaced the env mailer")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(envPassword)) != 1 {
		t.Fatal("a missing row replaced the env password")
	}
	from := written["from"].(map[string]any)
	if from["address"] != "env@example.test" || from["name"] != "Env" {
		t.Fatal("a missing row replaced the env from header")
	}
}

func TestDeliverKeepsTheEnvDocumentWhenTheReadFails(t *testing.T) {
	var written map[string]any
	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"mailers": map[string]any{"smtp": map[string]any{"host": "env-host"}},
				"from":    map[string]any{"address": "env@example.test", "name": "Env"},
			}
		},
		Write: func(doc map[string]any) { written = doc },
		SMTP: func(context.Context) (mailer.Dial, bool, error) {
			return mailer.Dial{Host: "127.0.0.1", Password: "stored-mailbox-secret", UseHost: true, UsePassword: true}, true, errors.New("db down")
		},
		From: func(context.Context) (mailer.From, bool, error) {
			return mailer.From{Address: "replaced@example.test", Name: "Replaced", UseAddress: true, UseName: true}, true, errors.New("db down")
		},
	})
	if err := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: &recordingMail{}}).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	smtp := written["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["host"] != "env-host" || smtp["password"] != nil {
		t.Fatal("a failed read replaced the env mailer")
	}
	from := written["from"].(map[string]any)
	if from["address"] != "env@example.test" || from["name"] != "Env" {
		t.Fatal("a failed read replaced the env from header")
	}
}

func TestQueueRefusesWithoutPublishingOrDialing(t *testing.T) {
	var written bool
	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any { return map[string]any{} },
		Write:    func(map[string]any) { written = true },
		Restore:  func() {},
		Observe:  func() {},
	})
	transport := &recordingMail{}
	err := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: transport}).Queue()
	if !errors.Is(err, errQueueRefused) {
		t.Fatalf("queue error = %v", err)
	}
	if transport.queued || written {
		t.Fatal("queue published the mail document or reached the transport")
	}
	if err := (*Facade)(nil).Queue(); !errors.Is(err, errQueueRefused) {
		t.Fatalf("nil facade queue error = %v", err)
	}
}

func TestNewMailerKeepsNilDependencies(t *testing.T) {
	cfg := mailer.NewConfig(mailer.Hooks{})
	var held bool
	got := NewMailer(MailerDeps{
		Config: cfg,
		Gate: func(run func() error) error {
			held = true
			return run()
		},
	})
	if got == nil || got.Config() != cfg {
		t.Fatal("the mailer dropped the config")
	}
	if err := got.Deliver(func() error { return nil }); err != nil || !held {
		t.Fatal("the mailer dropped the gate")
	}

	empty := NewMailer(MailerDeps{})
	if empty == nil || empty.Config() != nil || empty.gate != nil {
		t.Fatal("a missing dependency was filled in")
	}
}

func TestNewFacadeKeepsNilDependencies(t *testing.T) {
	mailer := &Mailer{}
	transport := &recordingMail{}
	got := NewFacade(FacadeDeps{Mailer: mailer, Inner: transport})
	if got == nil || got.Mailer() != mailer || got.inner != transport {
		t.Fatal("the facade dropped a dependency")
	}
	empty := NewFacade(FacadeDeps{})
	if empty == nil || empty.Mailer() != nil || empty.inner != nil {
		t.Fatal("a missing dependency was filled in")
	}
}

func TestDeliverAcceptsTheLogDriverWithoutDialing(t *testing.T) {
	const secret = "env-mailbox-secret"
	var written bool
	var observed bool
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"driver":   "log",
				"app_env":  "local",
				"password": secret,
				"mailers":  map[string]any{"smtp": map[string]any{"password": secret}},
			}
		},
		Write:   func(map[string]any) { written = true },
		Restore: func() {},
		Observe: func() { observed = true },
	})
	transport := &recordingMail{}
	err := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: transport}).Send()
	if err != nil {
		t.Fatal("log driver send failed")
	}
	if transport.sent {
		t.Fatal("log driver dialed")
	}
	if !written || !observed {
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

func TestDeliverRefusesTheLogDriverInProduction(t *testing.T) {
	const secret = "env-mailbox-secret"
	var written bool
	cfg := mailer.NewConfig(mailer.Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"driver":   "log",
				"app_env":  "production",
				"password": secret,
			}
		},
		Write:   func(map[string]any) { written = true },
		Restore: func() {},
		Observe: func() {},
	})
	transport := &recordingMail{}
	err := NewFacade(FacadeDeps{Mailer: NewMailer(MailerDeps{Config: cfg}), Inner: transport}).Send()
	if !errors.Is(err, config.ErrLogDriverRefused) {
		t.Fatal("production log driver was not refused")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("refusal included a credential")
	}
	if transport.sent || written {
		t.Fatal("refused log driver dialed or published")
	}
}

func TestDeliverRefusesAMissingMailer(t *testing.T) {
	if err := NewFacade(FacadeDeps{Inner: &recordingMail{}}).Send(); !errors.Is(err, errMailerRequired) {
		t.Fatal("a missing mailer was sent")
	}
	if err := (*Facade)(nil).Send(); !errors.Is(err, errMailerRequired) {
		t.Fatal("a nil facade was sent")
	}
}

type recordingMail struct {
	sent   bool
	queued bool
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
	return nil
}
func (m *recordingMail) Subject(string) contractsmail.Mail { return m }
func (m *recordingMail) To([]string) contractsmail.Mail    { return m }
