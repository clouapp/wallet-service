package config

import (
	"errors"
	"strings"
	"testing"
)

func TestMailDocumentLocalUsesMailpit(t *testing.T) {
	doc, err := mailDocument(mailEnv{AppEnv: "local"})
	if err != nil {
		t.Fatalf("local mail document: %v", err)
	}
	if doc["driver"] != mailDriverSMTP || doc["default"] != mailDriverSMTP {
		t.Fatal("local driver was not smtp")
	}
	if doc["host"] != mailpitHost || doc["port"] != mailpitPort || doc["encryption"] != mailpitEncryption {
		t.Fatal("local smtp did not use Mailpit")
	}
	smtp := doc["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["host"] != mailpitHost || smtp["port"] != mailpitPort || smtp["transport"] != mailDriverSMTP {
		t.Fatal("Mailpit credentials were not on the smtp mailer")
	}
	if doc["username"] != "" || doc["password"] != "" {
		t.Fatal("unset credentials were filled in")
	}
}

func TestMailDocumentKeepsConfiguredCredentials(t *testing.T) {
	const password = "mailbox-secret-from-config"
	doc, err := mailDocument(mailEnv{
		AppEnv:      "production",
		Driver:      "smtp",
		Host:        "smtp.example",
		Port:        2525,
		Encryption:  "starttls",
		Username:    "mailer",
		Password:    password,
		FromAddress: "from@example",
		FromName:    "Macro",
	})
	if err != nil {
		t.Fatalf("mail document: %v", err)
	}
	if doc["driver"] != mailDriverSMTP || doc["host"] != "smtp.example" || doc["port"] != 2525 {
		t.Fatal("driver or host was not taken from config")
	}
	if doc["encryption"] != "starttls" || doc["username"] != "mailer" || doc["password"] != password {
		t.Fatal("credentials were not taken from config")
	}
	smtp := doc["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["password"] != password || smtp["host"] != "smtp.example" {
		t.Fatal("smtp mailer did not keep the configured credentials")
	}
	from := doc["from"].(map[string]any)
	if from["address"] != "from@example" || from["name"] != "Macro" {
		t.Fatal("from header was not taken from config")
	}
}

func TestMailDocumentLocalLogDriverDoesNotUseMailpit(t *testing.T) {
	doc, err := mailDocument(mailEnv{AppEnv: "local", Driver: "log"})
	if err != nil {
		t.Fatalf("local log driver: %v", err)
	}
	if doc["driver"] != mailDriverLog || doc["app_env"] != "local" {
		t.Fatal("local log driver was not recorded")
	}
	if doc["host"] != "" || doc["port"] != defaultMailPort {
		t.Fatal("log driver was pointed at Mailpit")
	}
}

func TestMailDocumentRefusesLogDriverInProduction(t *testing.T) {
	const password = "mailbox-secret-from-config"
	for _, appEnv := range []string{"production", "prod", " PRODUCTION "} {
		_, err := mailDocument(mailEnv{AppEnv: appEnv, Driver: " LOG ", Password: password})
		if !errors.Is(err, ErrLogDriverRefused) {
			t.Fatalf("APP_ENV %q log driver error = %v", appEnv, err)
		}
		if err != nil && strings.Contains(err.Error(), password) {
			t.Fatal("refusal included a credential")
		}
	}
}

func TestMailDocumentAllowsLogDriverOutsideProduction(t *testing.T) {
	for _, appEnv := range []string{"local", "testing", "staging"} {
		doc, err := mailDocument(mailEnv{AppEnv: appEnv, Driver: "log"})
		if err != nil {
			t.Fatalf("APP_ENV %q log driver: %v", appEnv, err)
		}
		if doc["driver"] != mailDriverLog {
			t.Fatalf("APP_ENV %q driver = %v", appEnv, doc["driver"])
		}
	}
}

func TestMailDocumentKeepsAnExplicitLocalHost(t *testing.T) {
	doc, err := mailDocument(mailEnv{AppEnv: "local", Host: "mail.example", Port: 587, Encryption: "tls"})
	if err != nil {
		t.Fatalf("local mail document: %v", err)
	}
	if doc["host"] != "mail.example" || doc["port"] != 587 || doc["encryption"] != "tls" {
		t.Fatal("an explicit local host was replaced with Mailpit")
	}
}

func TestTestingMailDocumentKeepsTheSMTPDefaults(t *testing.T) {
	doc, err := mailDocument(mailEnv{AppEnv: "testing"})
	if err != nil {
		t.Fatalf("testing mail document: %v", err)
	}
	if doc["driver"] != mailDriverSMTP || doc["host"] != "" || doc["port"] != defaultMailPort || doc["encryption"] != defaultMailEncryption {
		t.Fatal("testing defaults changed")
	}
	from := doc["from"].(map[string]any)
	if from["address"] != defaultMailFromAddress || from["name"] != defaultMailFromName {
		t.Fatal("from defaults changed")
	}
}
