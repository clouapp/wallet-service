package mails

import (
	"fmt"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// UserInviteMail is sent when an account owner invites a new user.
type UserInviteMail struct {
	To          string
	InvitedBy   string
	AccountName string
	InviteLink  string
}

func (m *UserInviteMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: fmt.Sprintf("You've been invited to %s on Vault", m.AccountName),
	}
}

func (m *UserInviteMail) Content() *contractsmail.Content {
	return &contractsmail.Content{
		Html: fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:40px auto;color:#333;">
  <h2>You've been invited to %s</h2>
  <p>%s has invited you to collaborate on the <strong>%s</strong> account in Vault.</p>
  <p>Click the button below to accept the invitation and set up your account.</p>
  <p>
    <a href="%s" style="display:inline-block;padding:12px 24px;background:#1a56db;color:#fff;text-decoration:none;border-radius:4px;">
      Accept Invitation
    </a>
  </p>
  <p>This invitation link expires in 72 hours.</p>
  <p>If you were not expecting this invitation, you can safely ignore this email.</p>
</body>
</html>`, m.AccountName, m.InvitedBy, m.AccountName, m.InviteLink),
	}
}

func (m *UserInviteMail) Attachments() []string { return nil }
func (m *UserInviteMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "user-invite"}
}
func (m *UserInviteMail) Queue() *contractsmail.Queue { return nil }
