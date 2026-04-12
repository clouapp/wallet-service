package events

import "github.com/goravel/framework/contracts/event"

type WalletActivated struct{}

func (e *WalletActivated) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
