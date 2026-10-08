package listeners

import (
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/jobs"
)

// NewCredentialMailDispatcher enqueues reset and invite jobs with the process
// queue. Services receive it from the composition root, which binds the
// framework queue facade here.
func NewCredentialMailDispatcher() *jobs.CredentialMailDispatcher {
	return jobs.NewCredentialMailDispatcher(func() jobs.Enqueuer {
		return facades.Queue()
	})
}
