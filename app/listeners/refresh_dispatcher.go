package listeners

import (
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/services/refresh"
)

// NewRefreshDispatcher enqueues wallet refresh and reconcile jobs with the
// process queue. Console commands and services receive it from the composition
// root, which binds the framework queue facade here.
func NewRefreshDispatcher() refresh.Dispatcher {
	return jobs.NewDispatcher(func() jobs.Enqueuer { return facades.Queue() })
}
