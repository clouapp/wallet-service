package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/services/credentialmail"
)

// SendCredentialMailJob mints a reset or invite token at send time and calls
// Mail().Send. Welcome is not this job. The queue arguments are the subject
// id and the purpose.
type SendCredentialMailJob struct {
	service    *credentialmail.Service
	inviteLink *string
}

// NewSendCredentialMailJob binds the mail service. A job without one refuses
// every payload.
func NewSendCredentialMailJob(service *credentialmail.Service) *SendCredentialMailJob {
	return &SendCredentialMailJob{service: service}
}

func (j *SendCredentialMailJob) Signature() string {
	return "send_credential_mail"
}

// CredentialMailArgs is the only payload this job accepts: one JSON object
// with the subject id and the purpose.
func CredentialMailArgs(subjectID uuid.UUID, purpose string) ([]queue.Arg, error) {
	if subjectID == uuid.Nil {
		return nil, errors.New("send_credential_mail: subject id is required")
	}
	if !credentialmail.KnownPurpose(purpose) {
		return nil, errors.New("send_credential_mail: unknown purpose")
	}
	return encode(credentialMailPayload{SubjectID: subjectID, Purpose: purpose})
}

func (j *SendCredentialMailJob) Handle(args ...any) error {
	payload, err := decodeCredentialMailArgs(args)
	if err != nil {
		return err
	}
	mailer, err := j.mailer()
	if err != nil {
		return err
	}
	// The invite link is the return value of Send, captured for the sync
	// runner. It is not a queue argument.
	link, err := mailer.Send(context.Background(), payload.SubjectID, payload.Purpose)
	if j.inviteLink != nil {
		*j.inviteLink = link
	}
	return err
}

// mailer is the mail service the job was built with. A job built without one
// refuses the payload.
func (j *SendCredentialMailJob) mailer() (*credentialmail.Service, error) {
	if j.service == nil {
		return nil, errors.New("send_credential_mail: mail service is required")
	}
	return j.service, nil
}

// ShouldRetry refuses another attempt. A second run would mint another
// credential and invalidate the one already sent.
// SyncRunner runs one job with its queue arguments. Those arguments are the
// only values that would be stored on a queue.
type SyncRunner func(job queue.Job, args []queue.Arg) error

// DispatchSyncAccountInvite enqueues an invite as the invite id and the
// purpose, then returns the link the job minted. The link is not an argument.
func DispatchSyncAccountInvite(run SyncRunner, inviteID uuid.UUID, mailer *credentialmail.Service) (string, error) {
	if run == nil {
		return "", errors.New("send_credential_mail: runner is required")
	}
	if mailer == nil {
		return "", errors.New("send_credential_mail: mail service is required")
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

type credentialMailPayload struct {
	SubjectID uuid.UUID `json:"subject_id"`
	Purpose   string    `json:"purpose"`
}

func decodeCredentialMailArgs(args []any) (credentialMailPayload, error) {
	var payload credentialMailPayload
	if err := decode(args, &payload); err != nil {
		return credentialMailPayload{}, fmt.Errorf("send_credential_mail: %w", err)
	}
	if payload.SubjectID == uuid.Nil {
		return credentialMailPayload{}, errors.New("send_credential_mail: subject id is required")
	}
	if !credentialmail.KnownPurpose(payload.Purpose) {
		return credentialMailPayload{}, errors.New("send_credential_mail: purpose is not a credential mail purpose")
	}
	return payload, nil
}
