package providers

import (
	"context"
	"errors"

	"github.com/goravel/framework/contracts/foundation"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// CredentialMailServiceProvider binds credential mail. The queue payload is
// the subject id and the purpose. Send happens inside the job.
type CredentialMailServiceProvider struct{}

func (p *CredentialMailServiceProvider) Register(app foundation.Application) {
	app.Singleton((*credentialmail.Service)(nil), func(app foundation.Application) (any, error) {
		users, err := resolve[*usersvc.Service](app)
		if err != nil {
			return nil, err
		}
		tokens, err := resolve[*authsvc.Service](app)
		if err != nil {
			return nil, err
		}
		resets, err := resolve[*sessions.PasswordResets](app)
		if err != nil {
			return nil, err
		}
		invites, err := resolve[*account.Service](app)
		if err != nil {
			return nil, err
		}
		dispatcher := newCredentialMailDispatcher(app)
		return credentialmail.NewService(credentialmail.Deps{
			Users:          users,
			Tokens:         tokens,
			Resets:         resets,
			Invites:        invites,
			Sender:         credentialMailSender{},
			Dispatch:       dispatcher.Dispatch,
			DispatchInvite: dispatcher.DispatchAccountInvite,
		}), nil
	})
}

// newCredentialMailDispatcher enqueues reset and invite jobs with the process
// queue. The mail service is resolved when a mail is dispatched: it depends
// on the user and account services, which take this dispatcher.
func newCredentialMailDispatcher(app foundation.Application) *jobs.CredentialMailDispatcher {
	return jobs.NewCredentialMailDispatcher(
		func() jobs.Enqueuer { return appfacades.Queue() },
		func() (*credentialmail.Service, error) { return resolve[*credentialmail.Service](app) },
	)
}

func (p *CredentialMailServiceProvider) Boot(foundation.Application) {}

type credentialMailSender struct{}

func (credentialMailSender) SendInvite(ctx context.Context, message account.InviteMail) error {
	if ctx == nil {
		return errors.New("invite mail: context is required")
	}
	if message.To == "" || message.Link == "" {
		return errors.New("invite mail: recipient and link are required")
	}
	return appfacades.Mail().To([]string{message.To}).Send(&mails.UserInviteMail{
		To:          message.To,
		InvitedBy:   message.InvitedBy,
		AccountName: message.AccountName,
		InviteLink:  message.Link,
	})
}

func (credentialMailSender) SendWelcome(ctx context.Context, to, fullName string) error {
	if ctx == nil {
		return errors.New("welcome mail: context is required")
	}
	if to == "" {
		return errors.New("welcome mail: recipient is required")
	}
	return appfacades.Mail().To([]string{to}).Send(&mails.WelcomeMail{
		To:       to,
		FullName: fullName,
	})
}

func (credentialMailSender) SendReset(ctx context.Context, to, resetLink string) error {
	if ctx == nil {
		return errors.New("password reset mail: context is required")
	}
	if to == "" || resetLink == "" {
		return errors.New("password reset mail: recipient and link are required")
	}
	return appfacades.Mail().To([]string{to}).Send(&mails.PasswordResetMail{
		To:        to,
		ResetLink: resetLink,
	})
}
