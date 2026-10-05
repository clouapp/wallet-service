package withdrawalevents

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestNewRegistryDecimalsKeepsItsDependencies(t *testing.T) {
	t.Parallel()

	registry, rows := addedChainsRegistry()
	decimals := NewRegistryDecimals(RegistryDecimalsDeps{Registry: registry, Chains: rows})
	if decimals.registry != registry {
		t.Fatal("registry decimals did not keep the token registry")
	}
	chain, err := decimals.chains.FindByID(context.Background(), models.ChainBase)
	if err != nil || chain == nil || chain.NativeDecimals != 18 {
		t.Fatalf("registry decimals did not keep the chain store: chain=%v err=%v", chain, err)
	}
}

func TestNewRegistryDecimalsAllowsAbsentDependencies(t *testing.T) {
	t.Parallel()

	decimals := NewRegistryDecimals(RegistryDecimalsDeps{})
	if decimals.registry != nil || decimals.chains != nil {
		t.Fatal("absent dependencies were not stored as nil")
	}
	if _, ok := decimals.Decimals(models.ChainBase, models.NativeETH); ok {
		t.Fatal("absent registry resolved decimals")
	}
}
