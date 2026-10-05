package seeds

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestAddedChainTokenAndResourceInsertsAreIdempotent(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	if err := repositories.NewChainRepository(nil).Create(ctx, &models.Chain{
		ID: "base", Name: "Base", AdapterType: models.AdapterTypeEVM, NativeSymbol: "eth",
		NativeDecimals: 18, RpcURL: "sealed", RequiredConfirmations: 12, Status: "active",
	}); err != nil {
		t.Fatalf("create chain: %v", err)
	}

	token := tokenSeed{chainID: "base", symbol: "USDC", name: "USD Coin", contractAddress: "0xabc", decimals: 6, iconURL: "https://example.invalid/icon"}
	created, err := createTokenUnlessPresent(token)
	if err != nil || !created {
		t.Fatalf("create token: created=%v err=%v", created, err)
	}
	created, err = createTokenUnlessPresent(token)
	if err != nil || created {
		t.Fatalf("repeat token insert: created=%v err=%v", created, err)
	}

	tokens := repositories.NewTokenRepository(nil)
	if err := tokens.Create(ctx, &models.Token{
		ID: uuid.New(), ChainID: "base", Symbol: "OLD", Name: "Old",
		ContractAddress: "0xdef", Decimals: 6, Status: "disabled",
	}); err != nil {
		t.Fatalf("create disabled token: %v", err)
	}
	created, err = createTokenUnlessPresent(tokenSeed{
		chainID: "base", symbol: "OLD", name: "Old", contractAddress: "0xdef", decimals: 6, iconURL: "https://example.invalid/icon",
	})
	if err != nil || created {
		t.Fatalf("disabled token was inserted again: created=%v err=%v", created, err)
	}
	listed, err := tokens.FindByChainID(ctx, "base")
	if err != nil || len(listed) != 1 {
		t.Fatalf("active tokens = %d (%v), want the one inserted by the seed", len(listed), err)
	}

	resource := resourceSeed{chainID: "base", resourceType: "explorer", name: "Blockscout", url: "https://example.invalid/explorer"}
	created, err = createResourceUnlessPresent(resource)
	if err != nil || !created {
		t.Fatalf("create resource: created=%v err=%v", created, err)
	}
	created, err = createResourceUnlessPresent(resource)
	if err != nil || created {
		t.Fatalf("repeat resource insert: created=%v err=%v", created, err)
	}
	resources := repositories.NewChainResourceRepository(nil)
	if err := resources.Create(ctx, &models.ChainResource{
		ID: uuid.New(), ChainID: "base", Type: "faucet", Name: "Old faucet",
		URL: "https://example.invalid/faucet", Status: "disabled",
	}); err != nil {
		t.Fatalf("create disabled resource: %v", err)
	}
	created, err = createResourceUnlessPresent(resourceSeed{
		chainID: "base", resourceType: "faucet", name: "Old faucet", url: "https://example.invalid/other",
	})
	if err != nil || created {
		t.Fatalf("disabled resource was inserted again: created=%v err=%v", created, err)
	}
	active, err := resources.FindByChainID(ctx, "base")
	if err != nil || len(active) != 1 || active[0].Name != "Blockscout" {
		t.Fatalf("active resources = %+v (%v)", active, err)
	}
}
