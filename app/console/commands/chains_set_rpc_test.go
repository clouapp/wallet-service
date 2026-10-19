package commands

import (
	"testing"

	"github.com/goravel/framework/contracts/console/command"
)

func TestChains_Set_RPCSignature(t *testing.T) {
	cmd := NewChainsSetRPC(nil)
	if cmd.Signature() != "chains:set-rpc" {
		t.Fatalf("signature = %s", cmd.Signature())
	}
	if cmd.Description() == "" {
		t.Fatal("empty description")
	}
	ext := cmd.Extend()
	if ext.Category != "chains" {
		t.Fatalf("category = %s", ext.Category)
	}
	if len(ext.Arguments) != 2 {
		t.Fatalf("arguments = %d", len(ext.Arguments))
	}
	if len(ext.Flags) != 1 {
		t.Fatalf("flags = %d", len(ext.Flags))
	}
	flag, ok := ext.Flags[0].(*command.BoolFlag)
	if !ok || flag.Name != "skip-network-check" {
		t.Fatalf("flag = %#v, want --skip-network-check", ext.Flags[0])
	}
}
