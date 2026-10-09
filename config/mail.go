package config

import (
	"errors"
	"strings"

	"github.com/macrowallets/waas/app/facades"
)

const (
	mailDriverSMTP = "smtp"
	mailDriverLog  = "log"

	// Mailpit's SMTP listener. Local SMTP with no host of its own uses it.
	mailpitHost           = "127.0.0.1"
	mailpitPort           = 1025
	mailpitEncryption     = "none"
	defaultMailPort       = 587
	defaultMailEncryption = "tls"

	defaultMailFromAddress = "noreply@vault.dev"
	defaultMailFromName    = "Vault"
)

// ErrLogDriverRefused is returned when production is configured with the log driver.
var ErrLogDriverRefused = errors.New("mail: log driver is refused in production")

// mailEnv is the MAIL_* document before defaults. Empty strings and a zero
// port mean the variable was unset.
type mailEnv struct {
	AppEnv      string
	Driver      string
	Host        string
	Port        int
	Encryption  string
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

func registerMail() {
	doc, err := mailDocument(mailEnv{
		AppEnv:      envString("APP_ENV", "local"),
		Driver:      envString("MAIL_MAILER", ""),
		Host:        envString("MAIL_HOST", ""),
		Port:        envInt("MAIL_PORT", 0),
		Encryption:  envString("MAIL_ENCRYPTION", ""),
		Username:    envString("MAIL_USERNAME", ""),
		Password:    envString("MAIL_PASSWORD", ""),
		FromAddress: envString("MAIL_FROM_ADDRESS", ""),
		FromName:    envString("MAIL_FROM_NAME", ""),
	})
	if err != nil {
		panic(err)
	}
	facades.Config().Add("mail", doc)
}

// mailDocument builds the process mail config from MAIL_*. The driver is
// MAIL_MAILER (smtp or log). Host, port, encryption, username, and password
// are the credentials. Local SMTP with those credentials unset uses Mailpit.
// The log driver is refused when APP_ENV is production.
func mailDocument(in mailEnv) (map[string]any, error) {
	appEnv := strings.ToLower(strings.TrimSpace(in.AppEnv))
	if appEnv == "" {
		appEnv = "local"
	}
	driver := strings.ToLower(strings.TrimSpace(in.Driver))
	if driver == "" {
		driver = mailDriverSMTP
	}
	if err := RefuseLogDriver(appEnv, driver); err != nil {
		return nil, err
	}

	host := strings.TrimSpace(in.Host)
	port := in.Port
	encryption := strings.TrimSpace(in.Encryption)
	if appEnv == "local" && driver == mailDriverSMTP {
		if host == "" {
			host = mailpitHost
		}
		if port == 0 {
			port = mailpitPort
		}
		if encryption == "" {
			encryption = mailpitEncryption
		}
	}
	if port == 0 {
		port = defaultMailPort
	}
	if encryption == "" {
		encryption = defaultMailEncryption
	}
	fromAddress := strings.TrimSpace(in.FromAddress)
	if fromAddress == "" {
		fromAddress = defaultMailFromAddress
	}
	fromName := strings.TrimSpace(in.FromName)
	if fromName == "" {
		fromName = defaultMailFromName
	}
	username := in.Username
	password := in.Password

	return map[string]any{
		"driver":     driver,
		"app_env":    appEnv,
		"default":    driver,
		"host":       host,
		"port":       port,
		"encryption": encryption,
		"username":   username,
		"password":   password,
		"mailers": map[string]any{
			"smtp": map[string]any{
				"transport":  mailDriverSMTP,
				"host":       host,
				"port":       port,
				"encryption": encryption,
				"username":   username,
				"password":   password,
			},
			"log": map[string]any{
				"transport": mailDriverLog,
			},
		},
		"from": map[string]any{
			"address": fromAddress,
			"name":    fromName,
		},
	}, nil
}

// RefuseLogDriver reports the production refusal of the log driver.
// Any other driver, including smtp, is allowed.
func RefuseLogDriver(appEnv, driver string) error {
	if strings.EqualFold(strings.TrimSpace(driver), mailDriverLog) && productionAppEnv(appEnv) {
		return ErrLogDriverRefused
	}
	return nil
}

func productionAppEnv(appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "production", "prod":
		return true
	default:
		return false
	}
}
