package dtos

import "github.com/goravel/framework/contracts/event"

// Domain event markers live here so services and the composition root share
// one concrete type. Services may not import app/events; both may import dtos.
// Handle passes listener args through unchanged.

type WalletCreated struct{}

func (e *WalletCreated) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}

type WalletActivated struct{}

func (e *WalletActivated) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}

type DepositDetected struct{}

func (e *DepositDetected) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}

type WithdrawalBroadcasted struct{}

func (e *WithdrawalBroadcasted) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}

type WalletRefreshRequested struct{}

func (e *WalletRefreshRequested) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
