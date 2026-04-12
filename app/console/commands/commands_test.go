package commands

import (
	"testing"

	"github.com/goravel/framework/contracts/console/command"
)

func TestRefreshWalletSignature(t *testing.T) {
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

func TestRefreshAddressSignature(t *testing.T) {
	cmd := &RefreshAddress{}
	if cmd.Signature() != "refresh:address" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestRefreshCurrencySignature(t *testing.T) {
	cmd := &RefreshCurrency{}
	if cmd.Signature() != "refresh:currency" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestRefreshTxSignature(t *testing.T) {
	cmd := &RefreshTx{}
	if cmd.Signature() != "refresh:tx" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
}

func TestReconcileWalletSignature(t *testing.T) {
	cmd := &ReconcileWallet{}
	if cmd.Signature() != "reconcile:wallet" {
		t.Fatalf("unexpected: %s", cmd.Signature())
	}
	ext := cmd.Extend()
	if ext.Category != "reconcile" {
		t.Fatalf("expected category reconcile, got %s", ext.Category)
	}
}

func TestRefreshCurrencyAmbiguousDetection(t *testing.T) {
	for _, currency := range []string{"usdt", "usdc", "dai", "wbtc", "weth"} {
		if !ambiguousCurrencies[currency] {
			t.Errorf("expected %s to be ambiguous", currency)
		}
	}
	if ambiguousCurrencies["eth"] {
		t.Error("eth should not be ambiguous")
	}
}

func TestRefreshWalletHasExpectedFlags(t *testing.T) {
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
	for _, expected := range []string{"scope", "chain", "queue", "force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestRefreshAddressHasExpectedArguments(t *testing.T) {
	cmd := &RefreshAddress{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments, got %d", len(ext.Arguments))
	}
}

func TestRefreshCurrencyHasChainFlag(t *testing.T) {
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

func TestRefreshTxHasTwoArguments(t *testing.T) {
	cmd := &RefreshTx{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments (chain, tx_hash), got %d", len(ext.Arguments))
	}
}

func TestAllCommandDescriptionsNotEmpty(t *testing.T) {
	cmds := []interface{ Description() string }{
		&RefreshWallet{},
		&RefreshAddress{},
		&RefreshCurrency{},
		&RefreshTx{},
		&ReconcileWallet{},
	}
	for _, cmd := range cmds {
		if cmd.Description() == "" {
			t.Errorf("empty description for %T", cmd)
		}
	}
}

func TestRefreshAddressHasExpectedFlags(t *testing.T) {
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
	for _, expected := range []string{"scope", "queue", "force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestReconcileWalletHasExpectedFlags(t *testing.T) {
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
	for _, expected := range []string{"queue", "force", "reason"} {
		if !flagNames[expected] {
			t.Errorf("missing flag: %s", expected)
		}
	}
}

func TestAllRefreshCommandsCategoryIsRefresh(t *testing.T) {
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

func TestRefreshCurrencyHasTwoArguments(t *testing.T) {
	cmd := &RefreshCurrency{}
	ext := cmd.Extend()
	if len(ext.Arguments) != 2 {
		t.Fatalf("expected 2 arguments (currency, addresses), got %d", len(ext.Arguments))
	}
}
