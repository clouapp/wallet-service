package bootstrap

import (
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/services/credentialmail"
)

// Jobs are the queue jobs the application registers, each built with the
// services it runs. `artisan make:job` appends to the returned literal.
func Jobs() []queue.Job {
	return []queue.Job{
		jobs.NewSendCredentialMailJob(container.MustMake[*credentialmail.Service]()),
	}
}
