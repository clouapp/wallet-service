package commands

import (
	"testing"
)

func TestChainsSetRPCSignature(t *testing.T) {
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
}
