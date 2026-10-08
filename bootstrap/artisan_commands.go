package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	secretsadapter "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/chaincatalog"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/keyexport"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

const chainNetworkProfileConfigKey = "vault.chains.network_profile"

func seedMissingAddedChains(ctx context.Context, apply bool) (chainregistry.AddedChainsPlan, error) {
	plan, err := chaincatalog.New(facades.Config(), facades.Crypt()).SeedMissingAddedChains(ctx, apply)
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
	stored, err := settings.OpenStored(facades.Crypt(), encrypted)
	if err != nil {
		return "", err
	}
	return models.ResolveRPCURL(stored)
}

// walletChainLookup answers which chain a wallet is on for withdraw:preflight.
func walletChainLookup(wallets *walletrecords.Wallets) func(ctx context.Context, walletID uuid.UUID) (string, error) {
	return func(ctx context.Context, walletID uuid.UUID) (string, error) {
		wallet, err := wallets.FindByID(ctx, walletID)
		if err != nil || wallet == nil {
			return "", fmt.Errorf("wallet %s not found", walletID)
		}
		return wallet.Chain, nil
	}
}

// exportShareB opens the Secrets Manager reader when wallets:export-keys runs,
// so a process without Secrets Manager still serves the other commands.
func exportShareB() (keyexport.ShareBSource, error) {
	secrets, err := container.Make[*secretsmanager.Client]()
	if err != nil {
		return nil, errors.New("secrets manager is not configured; share B cannot be fetched")
	}
	return secretsadapter.NewShareB(secrets), nil
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
