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

func TestMergeMailFromKeepsEnvFieldsThatAreNotInUse(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"from": map[string]any{"address": "noreply@vault.dev", "name": "Vault"},
		"mailers": map[string]any{
			"smtp": map[string]any{"password": "env-mailbox-secret"},
		},
	}
	mergeMailFrom(cfg, MailFrom{Address: "from-header@example.test", UseAddress: true})
	from := cfg["from"].(map[string]any)
	if from["address"] != "from-header@example.test" {
		t.Fatal("the from address was not applied for the send")
	}
	if from["name"] != "Vault" {
		t.Fatal("an unused from name replaced the env header")
	}
	smtp := cfg["mailers"].(map[string]any)["smtp"].(map[string]any)
	if smtp["password"] != "env-mailbox-secret" {
		t.Fatal("the from header replaced the env mail password")
	}

	mergeMailFrom(cfg, MailFrom{Name: "Macro", UseName: true})
	if from["address"] != "from-header@example.test" || from["name"] != "Macro" {
		t.Fatal("the from name was not applied on its own")
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
