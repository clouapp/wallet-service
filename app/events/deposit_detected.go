package events

import "github.com/goravel/framework/contracts/event"

type DepositDetected struct{}

func (e *DepositDetected) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
