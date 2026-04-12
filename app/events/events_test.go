package events

import (
	"testing"

	"github.com/goravel/framework/contracts/event"
)

func TestWalletCreatedPassesArgsThrough(t *testing.T) {
	e := &WalletCreated{}
	args := []event.Arg{
		{Type: "string", Value: "wallet-1"},
		{Type: "string", Value: "eth"},
	}
	result, err := e.Handle(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d", len(result))
	}
	if result[0].Value != "wallet-1" {
		t.Fatalf("expected wallet-1, got %v", result[0].Value)
	}
	if result[1].Value != "eth" {
		t.Fatalf("expected eth, got %v", result[1].Value)
	}
}

func TestWalletActivatedPassesArgsThrough(t *testing.T) {
	e := &WalletActivated{}
	args := []event.Arg{
		{Type: "string", Value: "wallet-2"},
		{Type: "string", Value: "sol"},
	}
	result, err := e.Handle(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d", len(result))
	}
	if result[0].Value != "wallet-2" {
		t.Fatalf("expected wallet-2, got %v", result[0].Value)
	}
}

func TestDepositDetectedPassesArgsThrough(t *testing.T) {
	e := &DepositDetected{}
	args := []event.Arg{
		{Type: "string", Value: "wallet-3"},
		{Type: "string", Value: "btc"},
	}
	result, err := e.Handle(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d", len(result))
	}
	if result[0].Value != "wallet-3" {
		t.Fatalf("expected wallet-3, got %v", result[0].Value)
	}
}

func TestWithdrawalBroadcastedPassesArgsThrough(t *testing.T) {
	e := &WithdrawalBroadcasted{}
	args := []event.Arg{
		{Type: "string", Value: "wallet-4"},
		{Type: "string", Value: "polygon"},
	}
	result, err := e.Handle(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d", len(result))
	}
	if result[0].Value != "wallet-4" {
		t.Fatalf("expected wallet-4, got %v", result[0].Value)
	}
}

func TestWalletRefreshRequestedPassesArgsThrough(t *testing.T) {
	e := &WalletRefreshRequested{}
	args := []event.Arg{
		{Type: "string", Value: "wallet-5"},
		{Type: "string", Value: "eth"},
	}
	result, err := e.Handle(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d", len(result))
	}
	if result[0].Value != "wallet-5" {
		t.Fatalf("expected wallet-5, got %v", result[0].Value)
	}
}

func TestAllEventsHandleEmptyArgs(t *testing.T) {
	events := []struct {
		name string
		evt  interface {
			Handle([]event.Arg) ([]event.Arg, error)
		}
	}{
		{"WalletCreated", &WalletCreated{}},
		{"WalletActivated", &WalletActivated{}},
		{"DepositDetected", &DepositDetected{}},
		{"WithdrawalBroadcasted", &WithdrawalBroadcasted{}},
		{"WalletRefreshRequested", &WalletRefreshRequested{}},
	}
	for _, tc := range events {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.evt.Handle(nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != nil {
				t.Fatalf("expected nil for nil input, got %v", result)
			}
		})
	}
}

func TestAllEventsPreserveArgTypes(t *testing.T) {
	events := []struct {
		name string
		evt  interface {
			Handle([]event.Arg) ([]event.Arg, error)
		}
	}{
		{"WalletCreated", &WalletCreated{}},
		{"WalletActivated", &WalletActivated{}},
		{"DepositDetected", &DepositDetected{}},
		{"WithdrawalBroadcasted", &WithdrawalBroadcasted{}},
		{"WalletRefreshRequested", &WalletRefreshRequested{}},
	}
	args := []event.Arg{
		{Type: "string", Value: "id-1"},
		{Type: "int", Value: 42},
		{Type: "bool", Value: true},
	}
	for _, tc := range events {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.evt.Handle(args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != 3 {
				t.Fatalf("expected 3 args, got %d", len(result))
			}
			for i, a := range args {
				if result[i].Type != a.Type {
					t.Errorf("arg[%d]: expected type %q, got %q", i, a.Type, result[i].Type)
				}
				if result[i].Value != a.Value {
					t.Errorf("arg[%d]: expected value %v, got %v", i, a.Value, result[i].Value)
				}
			}
		})
	}
}
