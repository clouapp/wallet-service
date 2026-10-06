package jobs

import (
	"fmt"

	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/services/refresh"
)

const (
	blockchainConnection = "database"
	blockchainQueueName  = "blockchain"
)

// Enqueuer is the queue surface a refresh dispatch needs.
type Enqueuer interface {
	Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob
}

// Dispatcher puts refresh and reconcile jobs on the blockchain queue.
// Each payload is the wallet id and the chain id, on the database connection.
type Dispatcher struct {
	client func() Enqueuer
}

// NewDispatcher binds the queue the composition root already has. Callers
// pass that queue in so this package does not import the framework facades.
func NewDispatcher(client func() Enqueuer) *Dispatcher {
	return newDispatcher(client)
}

func newDispatcher(client func() Enqueuer) *Dispatcher {
	if client == nil {
		panic("wallet job dispatcher: queue client is required")
	}
	return &Dispatcher{client: client}
}

var _ refresh.Dispatcher = (*Dispatcher)(nil)

func (d *Dispatcher) DispatchBalances(walletID, chainID string) error {
	return d.dispatch(&RefreshWalletBalances{}, walletID, chainID)
}

func (d *Dispatcher) DispatchTransactions(walletID, chainID string) error {
	return d.dispatch(&RefreshWalletTransactions{}, walletID, chainID)
}

func (d *Dispatcher) DispatchTokens(walletID, chainID string) error {
	return d.dispatch(&RefreshWalletTokens{}, walletID, chainID)
}

func (d *Dispatcher) DispatchUTXOs(walletID, chainID string) error {
	return d.dispatch(&RefreshWalletUTXOs{}, walletID, chainID)
}

func (d *Dispatcher) DispatchReconcile(walletID, chainID string) error {
	return d.dispatch(&ReconcileWalletState{}, walletID, chainID)
}

func (d *Dispatcher) dispatch(job queue.Job, walletID, chainID string) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("wallet job dispatcher is not initialized")
	}
	args, err := WalletArgs(walletID, chainID)
	if err != nil {
		return err
	}
	return d.client().
		Job(job, args).
		OnConnection(blockchainConnection).
		OnQueue(blockchainQueueName).
		Dispatch()
}
