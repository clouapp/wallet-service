package listeners

import (
	"fmt"

	frameworkevent "github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/jobs"
)

type EnqueueWalletRefresh struct{}

func (l *EnqueueWalletRefresh) Signature() string {
	return "enqueue_wallet_refresh"
}

func (l *EnqueueWalletRefresh) Queue(args ...any) frameworkevent.Queue {
	_ = args
	return frameworkevent.Queue{
		Enable:     true,
		Connection: "database",
		Queue:      "blockchain",
	}
}

func (l *EnqueueWalletRefresh) Handle(args ...any) error {
	if len(args) < 2 {
		return fmt.Errorf("enqueue_wallet_refresh: expected wallet_id and chain_id")
	}
	walletID, ok0 := args[0].(string)
	chainID, ok1 := args[1].(string)
	if !ok0 || walletID == "" {
		return fmt.Errorf("enqueue_wallet_refresh: wallet_id must be a non-empty string")
	}
	if !ok1 || chainID == "" {
		return fmt.Errorf("enqueue_wallet_refresh: chain_id must be a non-empty string")
	}
	return facades.Queue().
		Job(&jobs.RefreshWalletBalances{}, []queue.Arg{
			{Type: "string", Value: walletID},
			{Type: "string", Value: chainID},
		}).
		OnConnection("database").
		OnQueue("blockchain").
		Dispatch()
}
