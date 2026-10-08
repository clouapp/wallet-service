package mails

import (
	"fmt"
	"html/template"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// UserInviteMail is sent when an account owner invites a new user.
type UserInviteMail struct {
	To          string
	InvitedBy   string
	AccountName string
	InviteLink  string
}

type userInviteData struct {
	AccountName string
	InvitedBy   string
	InviteLink  template.URL
}

func (m *UserInviteMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: fmt.Sprintf("You've been invited to %s on Vault", m.AccountName),
	}
}

func (m *UserInviteMail) Content() *contractsmail.Content {
	return &contractsmail.Content{
		Html: render("user_invite.html", userInviteData{
			AccountName: m.AccountName,
			InvitedBy:   m.InvitedBy,
			InviteLink:  template.URL(m.InviteLink),
		}),
	}
}

func (m *UserInviteMail) Attachments() []string { return nil }
func (m *UserInviteMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "user-invite"}
}
func (m *UserInviteMail) Queue() *contractsmail.Queue { return nil }
