package jobs

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/users"
)

// Enqueuer is the queue surface a credential mail dispatch needs.
type Enqueuer interface {
	Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob
}

// CredentialMailDispatcher enqueues a credential-mail job. The payload is the
// subject id and the purpose. An invite dispatch returns the link the job
// minted; that link is not a queue argument. The queue client is injected so
// this type does not call a framework facade.
type CredentialMailDispatcher struct {
	client func() Enqueuer
}

// NewCredentialMailDispatcher binds the queue client the composition root
// already has. A nil client is refused.
func NewCredentialMailDispatcher(client func() Enqueuer) *CredentialMailDispatcher {
	if client == nil {
		panic("credential mail dispatcher: queue client is required")
	}
	return &CredentialMailDispatcher{client: client}
}

var (
	_ account.InviteMailDispatcher = (*CredentialMailDispatcher)(nil)
	_ users.ResetMailDispatcher    = (*CredentialMailDispatcher)(nil)
)

// Dispatch enqueues one credential mail. The arguments are the subject id and
// the purpose.
func (d *CredentialMailDispatcher) Dispatch(subjectID uuid.UUID, purpose string) error {
	if d == nil || d.client == nil {
		return errors.New("credential mail dispatcher is not initialized")
	}
	args, err := CredentialMailArgs(subjectID, purpose)
	if err != nil {
		return err
	}
	return d.client().Job(&SendCredentialMailJob{}, args).DispatchSync()
}

// DispatchPasswordReset enqueues a reset mail for one user.
func (d *CredentialMailDispatcher) DispatchPasswordReset(userID uuid.UUID) error {
	return d.Dispatch(userID, credentialmail.PurposePasswordReset)
}

// DispatchAccountInvite enqueues an invite mail and returns the link the job
// minted. The link is not an argument.
func (d *CredentialMailDispatcher) DispatchAccountInvite(inviteID uuid.UUID) (string, error) {
	if d == nil || d.client == nil {
		return "", errors.New("credential mail dispatcher is not initialized")
	}
	return DispatchSyncAccountInvite(func(job queue.Job, args []queue.Arg) error {
		return d.client().Job(job, args).DispatchSync()
	}, inviteID, nil)
}
