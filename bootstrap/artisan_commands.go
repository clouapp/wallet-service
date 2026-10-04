package bootstrap

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/database/seeds"
)

func seedMissingAddedChains(ctx context.Context, apply bool) (commands.AddedChainsPlan, error) {
	plan, err := seeds.SeedMissingAddedChains(ctx, apply)
	if err != nil {
		return commands.AddedChainsPlan{}, err
	}
	chains := make([]commands.AddedChain, len(plan.Chains))
	for i, added := range plan.Chains {
		chains[i] = commands.AddedChain{
			ID: added.ID, EnvVar: added.EnvVar, Network: added.Network,
			NetworkID: added.NetworkID, IsTestnet: added.IsTestnet,
		}
	}
	return commands.AddedChainsPlan{
		Chains: chains, Tokens: plan.Tokens, Resources: plan.Resources, Skipped: plan.Skipped,
	}, nil
}

func evmCallSigner() evmcall.Signer {
	box := container.MustMake[*sweep.Box]()
	if box == nil || box.Service == nil {
		panic(fmt.Errorf("sweep service cannot sign evm calls"))
	}
	signer, ok := box.Service.(evmcall.Signer)
	if !ok || signer == nil {
		panic(fmt.Errorf("sweep service cannot sign evm calls"))
	}
	return signer
}
