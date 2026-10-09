package commands

import (
	"testing"

	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestRefresh_Wallet_Signature(t *testing.T) {
	cmd := &RefreshWallet{}
	if cmd.Signature() != "refresh:wallet" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
	ext := cmd.Extend()
	if ext.Category != "refresh" {
		t.Fatalf("expected category refresh, got %s", ext.Category)
	}
	if len(ext.Arguments) != 1 {
		t.Fatalf("expected 1 argument, got %d", len(ext.Arguments))
	}
}

func TestRefresh_Address_Signature(t *testing.T) {
	cmd := &RefreshAddress{}
	if cmd.Signature() != "refresh:address" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestRefresh_Currency_Signature(t *testing.T) {
	cmd := &RefreshCurrency{}
	if cmd.Signature() != "refresh:currency" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestRefresh_Tx_Signature(t *testing.T) {
	cmd := &RefreshTx{}
	if cmd.Signature() != "refresh:tx" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestScan_Deposits_Signature(t *testing.T) {
	cmd := &ScanDeposits{}
	if cmd.Signature() != "scan:deposits" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
	ext := cmd.Extend()
	if ext.Category != "deposit" {
		t.Fatalf("expected category deposit, got %s", ext.Category)
	}
	if len(ext.Arguments) != 1 {
		t.Fatalf("expected chain argument, got %d", len(ext.Arguments))
	}
}

func TestReconcile_Wallet_Signature(t *testing.T) {
	cmd := &ReconcileWallet{}
	if cmd.Signature() != "reconcile:wallet" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
	ext := cmd.Extend()
	if ext.Category != "reconcile" {
		t.Fatalf("expected category reconcile, got %s", ext.Category)
	}
}

func TestRefresh_Currency_AmbiguousDetection(t *testing.T) {
	for _, currency := range []string{"usdt", "usdc", "dai", "wbtc", "weth"} {
		if !refresh.AmbiguousCurrency(currency) {
			t.Errorf("expected %s to be ambiguous", currency)
		}
	}
	if refresh.AmbiguousCurrency("eth") {
		t.Error("eth should not be ambiguous")
	}
}

func TestRefresh_Wallet_HasExpectedFlags(t *testing.T) {
	cmd := &RefreshWallet{}
	ext := cmd.Extend()
	flagNames := make(map[string]bool)
	for _, f := range ext.Flags {
		switch ff := f.(type) {
		case *command.StringFlag:
			flagNames[ff.Name] = true
		case *command.BoolFlag:
			flagNames[ff.Name] = true
		}
	}
	for _, expected := range []string{"scope", "chain", "force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestRefresh_Address_HasExpectedArguments(t *testing.T) {
	cmd := &RefreshAddress{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments, got %d", len(ext.Arguments))
	}
}

func TestRefresh_Currency_HasChainFlag(t *testing.T) {
	cmd := &RefreshCurrency{}
	ext := cmd.Extend()
	hasChain := false
	for _, f := range ext.Flags {
		if sf, ok := f.(*command.StringFlag); ok && sf.Name == "chain" {
			hasChain = true
		}
	}
	if !hasChain {
		t.Fatal("refresh:currency must have --chain flag")
	}
}

func TestRefresh_Tx_HasTwoArguments(t *testing.T) {
	cmd := &RefreshTx{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments (chain, tx_hash), got %d", len(ext.Arguments))
	}
}

func TestAll_Command_DescriptionsNotEmpty(t *testing.T) {
	cmds := []interface{ Description() string }{
		&RefreshWallet{},
		&RefreshAddress{},
		&RefreshCurrency{},
		&RefreshTx{},
		&ScanDeposits{},
		&ReconcileWallet{},
	}
	for _, cmd := range cmds {
		if cmd.Description() == "" {
			t.Errorf("empty description for %T", cmd)
		}
	}
}

func TestRefresh_Address_HasExpectedFlags(t *testing.T) {
	cmd := &RefreshAddress{}
	ext := cmd.Extend()
	flagNames := make(map[string]bool)
	for _, f := range ext.Flags {
		switch ff := f.(type) {
		case *command.StringFlag:
			flagNames[ff.Name] = true
		case *command.BoolFlag:
			flagNames[ff.Name] = true
		}
	}
	for _, expected := range []string{"scope", "force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestReconcile_Wallet_HasExpectedFlags(t *testing.T) {
	cmd := &ReconcileWallet{}
	ext := cmd.Extend()
	flagNames := make(map[string]bool)
	for _, f := range ext.Flags {
		switch ff := f.(type) {
		case *command.StringFlag:
			flagNames[ff.Name] = true
		case *command.BoolFlag:
			flagNames[ff.Name] = true
		}
	}
	for _, expected := range []string{"force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestAll_Refresh_CommandsCategoryIsRefresh(t *testing.T) {
	cmds := []interface {
		Extend() command.Extend
	}{
		&RefreshWallet{},
		&RefreshAddress{},
		&RefreshCurrency{},
		&RefreshTx{},
	}
	for _, cmd := range cmds {
		ext := cmd.Extend()
		if ext.Category != "refresh" {
			t.Errorf("%T: expected category 'refresh', got %q", cmd, ext.Category)
		}
	}
}

func TestRefresh_Currency_HasTwoArguments(t *testing.T) {
	cmd := &RefreshCurrency{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments (currency, addresses), got %d", len(ext.Arguments))
	}
}

func TestRefresh_Commands_HaveNoQueueFlag(t *testing.T) {
	for name, cmd := range map[string]interface{ Extend() command.Extend }{
		"refresh:wallet":   &RefreshWallet{},
		"refresh:address":  &RefreshAddress{},
		"refresh:currency": &RefreshCurrency{},
		"refresh:tx":       &RefreshTx{},
		"reconcile:wallet": &ReconcileWallet{},
	} {
		for _, f := range cmd.Extend().Flags {
			var flagName string
			switch ff := f.(type) {
			case *command.StringFlag:
				flagName = ff.Name
			case *command.BoolFlag:
				flagName = ff.Name
			}
			if flagName == "queue" {
				t.Errorf("%s still has a --queue flag; the refresh queue has no consumer", name)
			}
		}
	}
}
