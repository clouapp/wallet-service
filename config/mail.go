package config

import "github.com/goravel/framework/facades"

func registerMail() {
	facades.Config().Add("mail", map[string]any{
		"default": "smtp",
		"mailers": map[string]any{
			"smtp": map[string]any{
				"transport":  "smtp",
				"host":       envString("MAIL_HOST", ""),
				"port":       envInt("MAIL_PORT", 587),
				"encryption": envString("MAIL_ENCRYPTION", "tls"),
				"username":   envString("MAIL_USERNAME", ""),
				"password":   envString("MAIL_PASSWORD", ""),
			},
		},
		"from": map[string]any{
			"address": envString("MAIL_FROM_ADDRESS", "noreply@vault.dev"),
			"name":    envString("MAIL_FROM_NAME", "Vault"),
		},
	})
}
