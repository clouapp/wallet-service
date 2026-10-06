package jobs

import (
	"fmt"

	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/app/dtos"
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

// EventBus is the event surface a domain-event dispatch needs.
type EventBus interface {
	Job(event event.Event, args []event.Arg) event.Task
}

// Dispatcher puts refresh and reconcile jobs on the blockchain queue and
// fires the domain events registered for those jobs. Each queue payload is
// the wallet id and the chain id, on the database connection.
type Dispatcher struct {
	client func() Enqueuer
	events func() EventBus
}

// NewDispatcher binds the queue and the event bus the composition root
// already has. Callers pass both in so this package does not import the
// framework facades. A nil event bus skips the domain event.
func NewDispatcher(client func() Enqueuer, events func() EventBus) *Dispatcher {
	return newDispatcher(client, events)
}

func newDispatcher(client func() Enqueuer, events func() EventBus) *Dispatcher {
	if client == nil {
		panic("wallet job dispatcher: queue client is required")
	}
	return &Dispatcher{client: client, events: events}
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

func (d *Dispatcher) DispatchWalletCreated(walletID, chainID string) error {
	bus := d.eventBus()
	if bus == nil {
		return nil
	}
	return bus.Job(&dtos.WalletCreated{}, walletChainArgs(walletID, chainID)).Dispatch()
}

func (d *Dispatcher) DispatchWalletActivated(walletID, chainID string) error {
	bus := d.eventBus()
	if bus == nil {
		return nil
	}
	return bus.Job(&dtos.WalletActivated{}, walletChainArgs(walletID, chainID)).Dispatch()
}

func (d *Dispatcher) DispatchWithdrawalBroadcasted(walletID, chainID string) error {
	bus := d.eventBus()
	if bus == nil {
		return nil
	}
	return bus.Job(&dtos.WithdrawalBroadcasted{}, walletChainArgs(walletID, chainID)).Dispatch()
}

func (d *Dispatcher) DispatchDepositDetected(walletID, chainID, txHash string) error {
	bus := d.eventBus()
	if bus == nil {
		return nil
	}
	return bus.Job(&dtos.DepositDetected{}, []event.Arg{
		{Type: "string", Value: walletID},
		{Type: "string", Value: chainID},
		{Type: "string", Value: txHash},
	}).Dispatch()
}

func walletChainArgs(walletID, chainID string) []event.Arg {
	return []event.Arg{
		{Type: "string", Value: walletID},
		{Type: "string", Value: chainID},
	}
}

func (d *Dispatcher) eventBus() EventBus {
	if d == nil || d.events == nil {
		return nil
	}
	return d.events()
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
