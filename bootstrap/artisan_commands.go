package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/database/seeds"
)

const chainNetworkProfileConfigKey = "vault.chains.network_profile"

func seedMissingAddedChains(ctx context.Context, apply bool) (chainregistry.AddedChainsPlan, error) {
	plan, err := seeds.SeedMissingAddedChains(ctx, apply)
	if err != nil {
		return chainregistry.AddedChainsPlan{}, err
	}
	chains := make([]chainregistry.AddedChain, len(plan.Chains))
	for i, added := range plan.Chains {
		chains[i] = chainregistry.AddedChain{
			ID: added.ID, AdapterType: added.AdapterType, EnvVar: added.EnvVar, Network: added.Network,
			NetworkID: added.NetworkID, IsTestnet: added.IsTestnet,
		}
	}
	return chainregistry.AddedChainsPlan{
		Chains: chains, Tokens: plan.Tokens, Resources: plan.Resources, Skipped: plan.Skipped,
	}, nil
}

func decryptChainRPC(encrypted string) (string, error) {
	stored, err := facades.Crypt().DecryptString(encrypted)
	if err != nil {
		return "", err
	}
	return models.ResolveRPCURL(stored)
}

func configuredChainProfile() string {
	return strings.TrimSpace(facades.Config().GetString(chainNetworkProfileConfigKey))
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
