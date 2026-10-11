package models

import "testing"

func TestFinality_FitsAdapter_OnlyWhereTheChainCanReadIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		policy, adapter string
		want            bool
	}{
		{FinalityConfirmations, AdapterTypeEVM, true},
		{FinalityConfirmations, AdapterTypeBitcoin, true},
		{FinalityConfirmations, AdapterTypeTron, true},
		{FinalityFinalizedTag, AdapterTypeEVM, true},
		{FinalityFinalizedTag, AdapterTypeTron, false},
		{FinalityFinalizedTag, AdapterTypeSolana, false},
		{FinalitySolidified, AdapterTypeTron, true},
		{FinalitySolidified, AdapterTypeEVM, false},
		{FinalitySolidified, AdapterTypeXRP, false},
		{"fast", AdapterTypeEVM, false},
		{"", AdapterTypeEVM, false},
	}
	for _, tc := range cases {
		if got := FinalityFitsAdapter(tc.policy, tc.adapter); got != tc.want {
			t.Errorf("FinalityFitsAdapter(%q, %q) = %v, want %v", tc.policy, tc.adapter, got, tc.want)
		}
		if got := IsFinalityPolicy(tc.policy); got != (tc.policy == FinalityConfirmations || tc.policy == FinalityFinalizedTag || tc.policy == FinalitySolidified) {
			t.Errorf("IsFinalityPolicy(%q) = %v", tc.policy, got)
		}
	}
}

func TestTransaction_FinalityPolicy_ARowWithoutOneCountsConfirmations(t *testing.T) {
	t.Parallel()

	if got := (&Transaction{}).FinalityPolicy(); got != FinalityConfirmations {
		t.Fatalf("empty finality reads %q, want %q", got, FinalityConfirmations)
	}
	if got := (&Transaction{Finality: FinalitySolidified}).FinalityPolicy(); got != FinalitySolidified {
		t.Fatalf("stored finality reads %q", got)
	}
}
