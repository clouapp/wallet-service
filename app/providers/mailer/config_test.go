package mailer

import (
	"context"
	"crypto/subtle"
	"errors"
	"testing"
)

func TestMergeDialKeepsEnvFieldsThatAreNotInUse(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"from": map[string]any{"address": "noreply@vault.dev"},
		"mailers": map[string]any{
			"smtp": map[string]any{
				"transport":  "smtp",
				"host":       "env-host",
				"port":       587,
				"encryption": "tls",
				"username":   "env-user",
				"password":   "env-mailbox-secret",
			},
		},
	}
	mergeDial(cfg, Dial{
		Host: "127.0.0.1", Port: 2525, Encryption: "starttls", Username: "mailer",
		UseHost: true, UsePort: true, UseEncryption: true, UseUsername: true,
	})
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" {
		t.Fatal("the dial left SMTP")
	}
	if smtp["host"] != "127.0.0.1" || cfg["host"] != "127.0.0.1" {
		t.Fatal("host was not applied for the send")
	}
	if smtp["port"] != 2525 || cfg["port"] != 2525 {
		t.Fatal("port was not applied for the send")
	}
	if smtp["encryption"] != "starttls" || cfg["encryption"] != "starttls" {
		t.Fatal("encryption was not applied for the send")
	}
	if smtp["username"] != "mailer" {
		t.Fatal("username was not applied for the send")
	}
	if smtp["password"] != "env-mailbox-secret" || cfg["password"] != nil {
		t.Fatal("an unused password replaced the env mailer")
	}
	if cfg["from"].(map[string]any)["address"] != "noreply@vault.dev" {
		t.Fatal("the from address was rewritten")
	}
}

func TestMergeFromKeepsEnvFieldsThatAreNotInUse(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"from": map[string]any{"address": "noreply@vault.dev", "name": "Vault"},
		"mailers": map[string]any{
			"smtp": map[string]any{"transport": "smtp", "password": "env-mailbox-secret"},
		},
	}
	mergeFrom(cfg, From{Address: "from-header@example.test", UseAddress: true})
	from := cfg["from"].(map[string]any)
	if from["address"] != "from-header@example.test" {
		t.Fatal("the from address was not applied for the send")
	}
	if from["name"] != "Vault" {
		t.Fatal("an unused from name replaced the env header")
	}
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" || smtp["password"] != "env-mailbox-secret" {
		t.Fatal("the from header replaced the env mailer")
	}

	mergeFrom(cfg, From{Name: "Macro", UseName: true})
	if from["address"] != "from-header@example.test" || from["name"] != "Macro" {
		t.Fatal("the from name was not applied on its own")
	}
}

func TestMergeDialAppliesAPasswordOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	const stored = "stored-mailbox-secret"
	cfg := map[string]any{
		"mailers": map[string]any{"smtp": map[string]any{"password": "env-mailbox-secret"}},
	}
	mergeDial(cfg, Dial{Password: stored, UsePassword: true})
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
	gotSMTP, _ := smtp["password"].(string)
	gotRoot, _ := cfg["password"].(string)
	if subtle.ConstantTimeCompare([]byte(gotSMTP), []byte(stored)) != 1 ||
		subtle.ConstantTimeCompare([]byte(gotRoot), []byte(stored)) != 1 {
		t.Fatal("the sealed password was not opened onto the mailer")
	}
}

func TestResolveKeepsTheEnvDocumentWhenTheReaderIsUnset(t *testing.T) {
	t.Parallel()

	const envPassword = "env-mailbox-secret"
	cfg := NewConfig(Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"from": map[string]any{"address": "env@example.test", "name": "Env"},
				"mailers": map[string]any{
					"smtp": map[string]any{"transport": "smtp", "host": "env-host", "password": envPassword},
				},
			}
		},
	})
	resolved := cfg.Resolve(context.Background())
	if resolved["host"] != nil || resolved["password"] != nil {
		t.Fatal("an unset reader wrote over the env mailer")
	}
	smtp := resolved["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" || smtp["host"] != "env-host" {
		t.Fatal("an unset reader replaced the env mailer")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(envPassword)) != 1 {
		t.Fatal("an unset reader replaced the env password")
	}
	from := resolved["from"].(map[string]any)
	if from["address"] != "env@example.test" || from["name"] != "Env" {
		t.Fatal("an unset reader replaced the env from header")
	}
}

func TestResolveKeepsTheEnvDocumentWhenTheReaderFails(t *testing.T) {
	t.Parallel()

	cfg := NewConfig(Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"mailers": map[string]any{"smtp": map[string]any{"host": "env-host"}},
				"from":    map[string]any{"address": "env@example.test"},
			}
		},
		SMTP: func(context.Context) (Dial, bool, error) {
			return Dial{Host: "127.0.0.1", UseHost: true, Password: "stored-mailbox-secret", UsePassword: true}, true, errors.New("db down")
		},
		From: func(context.Context) (From, bool, error) {
			return From{Address: "replaced@example.test", UseAddress: true}, true, errors.New("db down")
		},
	})
	resolved := cfg.Resolve(context.Background())
	smtp := resolved["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["host"] != "env-host" || smtp["password"] != nil || resolved["password"] != nil {
		t.Fatal("a failed read replaced the env mailer")
	}
	if resolved["from"].(map[string]any)["address"] != "env@example.test" {
		t.Fatal("a failed read replaced the env from header")
	}
}

func TestResolveAppliesSMTPAndFromWhenTheRowsExist(t *testing.T) {
	t.Parallel()

	const stored = "stored-mailbox-secret"
	cfg := NewConfig(Hooks{
		Baseline: func() map[string]any {
			return map[string]any{
				"mailers": map[string]any{"smtp": map[string]any{"transport": "smtp", "host": "env-host"}},
				"from":    map[string]any{"address": "env@example.test", "name": "Env"},
			}
		},
		SMTP: func(context.Context) (Dial, bool, error) {
			return Dial{
				Host: "127.0.0.1", Port: 2525, Encryption: "starttls", Username: "mailer",
				Password: stored, UseHost: true, UsePort: true, UseEncryption: true,
				UseUsername: true, UsePassword: true,
			}, true, nil
		},
		From: func(context.Context) (From, bool, error) {
			return From{Address: "from-header@example.test", Name: "Macro", UseAddress: true, UseName: true}, true, nil
		},
	})
	resolved := cfg.Resolve(context.Background())
	smtp := resolved["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["transport"] != "smtp" || smtp["host"] != "127.0.0.1" || resolved["host"] != "127.0.0.1" {
		t.Fatal("mail_smtp was not applied")
	}
	if smtp["port"] != 2525 || smtp["encryption"] != "starttls" || smtp["username"] != "mailer" {
		t.Fatal("mail_smtp left a field on the env mailer")
	}
	got, _ := smtp["password"].(string)
	if subtle.ConstantTimeCompare([]byte(got), []byte(stored)) != 1 {
		t.Fatal("mail_smtp password was not applied")
	}
	from := resolved["from"].(map[string]any)
	if from["address"] != "from-header@example.test" || from["name"] != "Macro" {
		t.Fatal("mail_delivery was not applied")
	}
}
