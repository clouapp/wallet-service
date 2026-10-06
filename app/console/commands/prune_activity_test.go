package commands

import "testing"

func TestPrune_Activity_Signature(t *testing.T) {
	cmd := NewPruneActivity(nil)
	if cmd.Signature() != "activity:prune" {
		t.Fatalf("signature = %s", cmd.Signature())
	}
	if cmd.Description() == "" {
		t.Fatal("empty description")
	}
}
