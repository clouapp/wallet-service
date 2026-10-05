package mail

import (
	"context"
	"crypto/subtle"
	"errors"
	"testing"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/providers/mailer"
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
	facade := NewFacade(FacadeDeps{Mailer: NewMailer(cfg, nil), Inner: transport})
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
	if err := NewFacade(FacadeDeps{Mailer: NewMailer(cfg, nil), Inner: transport}).Send(); err != nil {
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
	if err := NewFacade(FacadeDeps{Mailer: NewMailer(cfg, nil), Inner: &recordingMail{}}).Send(); err != nil {
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
	err := NewFacade(FacadeDeps{Mailer: NewMailer(cfg, nil), Inner: transport}).Queue()
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
