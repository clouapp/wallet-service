package ramps

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestOfframpMatchesDepositAmountAndChain(t *testing.T) {
	chain := "solana"
	tx := &models.Transaction{
		TargetToken: "BRS",
		Chain:       &chain,
		AmountToken: "17.000000000000000000",
	}
	dep := ramp.ManagedWalletDeposit{
		Amount: "17", Currency: "BRS", Network: "solana",
	}
	require.True(t, offrampMatchesDeposit(tx, dep, "0"))
}

func TestOfframpMatchesDepositRejectsDifferentOrderAmount(t *testing.T) {
	chain := "solana"
	tx := &models.Transaction{
		TargetToken: "BRS",
		Chain:       &chain,
		AmountToken: "16.000000000000000000",
	}
	dep := ramp.ManagedWalletDeposit{
		Amount: "17.000000", Currency: "BRS", Network: "solana",
	}
	require.False(t, offrampMatchesDeposit(tx, dep, "0"))
}

func TestOfframpMatchesDepositRejectsLowAmount(t *testing.T) {
	chain := "solana"
	tx := &models.Transaction{
		TargetToken: "BRS",
		Chain:       &chain,
		AmountToken: "17",
	}
	dep := ramp.ManagedWalletDeposit{Amount: "16.99", Currency: "BRS", Network: "solana"}
	require.False(t, offrampMatchesDeposit(tx, dep, "0"))
}
