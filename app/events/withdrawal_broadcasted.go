package events

import "github.com/goravel/framework/contracts/event"

type WithdrawalBroadcasted struct{}

func (e *WithdrawalBroadcasted) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
