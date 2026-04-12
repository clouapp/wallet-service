package events

import "github.com/goravel/framework/contracts/event"

type WalletRefreshRequested struct{}

func (e *WalletRefreshRequested) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
