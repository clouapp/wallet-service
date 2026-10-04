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
// Mail().Send. The queue arguments are the subject id and the purpose.
type SendCredentialMailJob struct {
	service *credentialmail.Service
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
	case credentialmail.PurposeAccountInvite:
		_, err := mailer.SendAccountInvite(context.Background(), subjectID)
		return err
	default:
		return fmt.Errorf("send_credential_mail: unknown purpose %q", purpose)
	}
}

// ShouldRetry refuses another attempt. A second run would mint another
// credential and invalidate the one already sent.
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
