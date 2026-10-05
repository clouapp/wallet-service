package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/credentialmail"
)

// SendCredentialMailJob mints a reset or invite token at send time and calls
// Mail().Send. Welcome loads the user and sends no credential. The queue
// arguments are the subject id and the purpose.
type SendCredentialMailJob struct {
	service    *credentialmail.Service
	inviteLink *string
}

// NewSendCredentialMailJob binds the mail service. A nil service is resolved
// from the container when the queue runs the registered job.
func NewSendCredentialMailJob(service *credentialmail.Service) *SendCredentialMailJob {
	return &SendCredentialMailJob{service: service}
}

func (j *SendCredentialMailJob) Signature() string {
	return "send_credential_mail"
}

// CredentialMailArgs is the only payload this job accepts. A token, link, or
// address is refused by the purpose check and by the two-argument shape.
func CredentialMailArgs(subjectID uuid.UUID, purpose string) ([]queue.Arg, error) {
	if subjectID == uuid.Nil {
		return nil, errors.New("send_credential_mail: subject id is required")
	}
	if !credentialmail.KnownPurpose(purpose) {
		return nil, errors.New("send_credential_mail: unknown purpose")
	}
	return []queue.Arg{
		{Type: "string", Value: subjectID.String()},
		{Type: "string", Value: purpose},
	}, nil
}

func (j *SendCredentialMailJob) Handle(args ...any) error {
	subjectID, purpose, err := decodeCredentialMailArgs(args)
	if err != nil {
		return err
	}
	mailer := j.service
	if mailer == nil {
		mailer = container.MustMake[*credentialmail.Service]()
	}
	switch purpose {
	case credentialmail.PurposePasswordReset:
		return mailer.SendPasswordReset(context.Background(), subjectID)
	case credentialmail.PurposeWelcome:
		return mailer.SendWelcome(context.Background(), subjectID)
	case credentialmail.PurposeAccountInvite:
		link, err := mailer.SendAccountInvite(context.Background(), subjectID)
		if j.inviteLink != nil {
			*j.inviteLink = link
		}
		return err
	default:
		return fmt.Errorf("send_credential_mail: unknown purpose %q", purpose)
	}
}

// ShouldRetry refuses another attempt. A second run would mint another
// credential and invalidate the one already sent.
// SyncRunner runs one job with its queue arguments. Those arguments are the
// only values that would be stored on a queue.
type SyncRunner func(job queue.Job, args []queue.Arg) error

// DispatchSyncAccountInvite enqueues an invite as the invite id and the
// purpose, then returns the link the job minted. The link is not an argument.
// A nil mailer is resolved from the container when the job runs.
func DispatchSyncAccountInvite(run SyncRunner, inviteID uuid.UUID, mailer *credentialmail.Service) (string, error) {
	if run == nil {
		return "", errors.New("send_credential_mail: runner is required")
	}
	args, err := CredentialMailArgs(inviteID, credentialmail.PurposeAccountInvite)
	if err != nil {
		return "", err
	}
	var link string
	job := &SendCredentialMailJob{service: mailer, inviteLink: &link}
	if err := run(job, args); err != nil {
		return link, err
	}
	return link, nil
}

func (j *SendCredentialMailJob) ShouldRetry(error, int) (bool, time.Duration) {
	return false, 0
}

func decodeCredentialMailArgs(args []any) (uuid.UUID, string, error) {
	if len(args) != 2 {
		return uuid.Nil, "", fmt.Errorf("send_credential_mail: expected 2 args (subject_id, purpose)")
	}
	subjectRaw, ok := args[0].(string)
	if !ok || subjectRaw == "" {
		return uuid.Nil, "", errors.New("send_credential_mail: subject_id must be a non-empty string")
	}
	purpose, ok := args[1].(string)
	if !ok || !credentialmail.KnownPurpose(purpose) {
		return uuid.Nil, "", errors.New("send_credential_mail: purpose is not a credential mail purpose")
	}
	subjectID, err := uuid.Parse(subjectRaw)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("send_credential_mail: invalid subject_id: %w", err)
	}
	if subjectID == uuid.Nil {
		return uuid.Nil, "", errors.New("send_credential_mail: subject id is required")
	}
	return subjectID, purpose, nil
}
