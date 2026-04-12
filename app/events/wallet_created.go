package events

import "github.com/goravel/framework/contracts/event"

type WalletCreated struct{}

func (e *WalletCreated) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
