package facades

import "testing"

func TestMergeMailDialKeepsEnvFieldsThatAreNotInUse(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"from": map[string]any{"address": "noreply@vault.dev"},
		"mailers": map[string]any{
			"smtp": map[string]any{
				"host":       "env-host",
				"port":       587,
				"encryption": "tls",
				"username":   "env-user",
				"password":   "env-mailbox-secret",
			},
		},
	}
	mergeMailDial(cfg, MailDial{
		Host: "127.0.0.1", Port: 2525, Encryption: "starttls", Username: "mailer",
		UseHost: true, UsePort: true, UseEncryption: true, UseUsername: true,
	})
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
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

func TestMergeMailDialAppliesAPasswordOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"mailers": map[string]any{"smtp": map[string]any{"password": "env-mailbox-secret"}},
	}
	mergeMailDial(cfg, MailDial{Password: "stored-mailbox-secret", UsePassword: true})
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["password"] != "stored-mailbox-secret" || cfg["password"] != "stored-mailbox-secret" {
		t.Fatal("the sealed password was not opened onto the mailer")
	}
}
