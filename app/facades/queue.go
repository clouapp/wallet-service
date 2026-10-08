package facades

import (
	contractsqueue "github.com/goravel/framework/contracts/queue"
	goravelfacades "github.com/goravel/framework/facades"
)

// Queue returns the process queue.
func Queue() contractsqueue.Queue {
	return goravelfacades.Queue()
}
