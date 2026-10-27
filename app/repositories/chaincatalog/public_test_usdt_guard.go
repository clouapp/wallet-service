package chaincatalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// ErrPublicTestUSDTConflict refuses an ambiguous logical symbol or a physical
// network drift. Operators retain their rows and must resolve the conflict.
var ErrPublicTestUSDTConflict = errors.New("public test USDT catalog conflict")

type publicTestTarget struct {
	chainID, network, adapter string
	networkID                 *int64
}

type publicTestCheckMode uint8

const (
	publicTestAlignment publicTestCheckMode = iota
	publicTestFullSeed
	publicTestAddedChainSeed
)

func publicTestTargets() []publicTestTarget {
	return []publicTestTarget{
		{models.ChainPolygon, models.NetworkPolygonAmoy, models.AdapterTypeEVM, i64p(models.EVMNetworkIDPolygonAmoy)},
		{models.ChainSOL, models.NetworkSolanaDevnet, models.AdapterTypeSolana, nil},
		{models.ChainArbitrum, models.NetworkArbitrumSepolia, models.AdapterTypeEVM, i64p(models.EVMNetworkIDArbitrumSepolia)},
	}
}

// preflightPublicTestUSDT checks the complete three-asset set before any catalog
// write. Apply takes chain locks and rechecks current rows inside its transaction;
// preview remains read-only. Seeding cannot rely on a requested profile alone.
func (cat *Catalog) preflightPublicTestUSDT(ctx context.Context, lock bool, mode publicTestCheckMode) error {
	profile, err := cat.profile()
	if err != nil {
		return err
	}
	chains := repositories.NewChainRepository(nil)
	tokens := repositories.NewTokenRepository(nil)
	for _, target := range publicTestTargets() {
		var row *models.Chain
		if lock {
			row, err = chains.LockByID(ctx, target.chainID)
		} else {
			row, err = chains.FindByID(ctx, target.chainID)
		}
		if errors.Is(err, models.ErrRepositoryNotFound) {
			if mode == publicTestFullSeed && profile == models.ChainNetworkProfileTestnet {
				return fmt.Errorf("%w: %s physical chain is missing", ErrPublicTestUSDTConflict, target.chainID)
			}
			continue
		}
		if err != nil {
			return err
		}
		actual := row.Network()
		if actual != target.network {
			if profile == models.ChainNetworkProfileTestnet {
				return fmt.Errorf("%w: %s physical network does not match %s", ErrPublicTestUSDTConflict, target.chainID, target.network)
			}
			continue
		}
		if row.AdapterType != target.adapter || !row.IsTestnet || !sameNetworkID(row.NetworkID, target.networkID) {
			return fmt.Errorf("%w: %s physical test identity differs", ErrPublicTestUSDTConflict, target.chainID)
		}
		rows, readErr := tokens.FindAllByChainID(ctx, target.chainID)
		if readErr != nil {
			return readErr
		}
		allowedRetirements := map[string]bool{}
		if mode == publicTestAlignment {
			mainnet, _, mainnetErr := models.PrimaryChainNetwork(models.ChainNetworkProfileMainnet, target.chainID)
			if mainnetErr != nil {
				return mainnetErr
			}
			changes, _ := planNetworkTokens(target.chainID, target.network, rows, tokensOfNetwork(target.network), tokensOfNetwork(mainnet.Network))
			for _, change := range changes {
				if change.Action == TokenActionDeactivate {
					allowedRetirements[change.Contract] = true
				}
			}
		}
		seed := publicTestUSDT[target.network].seed
		for _, token := range rows {
			if token.Status != models.TokenStatusActive || !strings.EqualFold(token.Symbol, models.SymbolUSDT) || sameContract(token.ContractAddress, seed.contractAddress) || allowedRetirements[token.ContractAddress] {
				continue
			}
			return fmt.Errorf("%w: %s already has an active operator USDT contract", ErrPublicTestUSDTConflict, target.chainID)
		}
	}
	return nil
}

func sameNetworkID(actual, expected *int64) bool {
	if actual == nil || expected == nil {
		return actual == nil && expected == nil
	}
	return *actual == *expected
}

// guardPublicTestSeed also protects the shared insertion seam used when a new
// added chain is seeded. Existing operator rows are never rewritten by that seam.
// The full seeding/alignment paths already hold all three chain locks.
func guardPublicTestSeed(ctx context.Context, seed tokenSeed) error {
	for _, target := range publicTestTargets() {
		public := publicTestUSDT[target.network].seed
		if !sameContract(seed.contractAddress, public.contractAddress) {
			continue
		}
		if seed.chainID != target.chainID || seed.symbol != models.SymbolUSDT || seed.decimals != 6 {
			return fmt.Errorf("%w: public test seed is not its canonical chain/asset pair", ErrPublicTestUSDTConflict)
		}
		chain, err := repositories.NewChainRepository(nil).FindByID(ctx, target.chainID)
		if err != nil {
			return err
		}
		if chain.AdapterType != target.adapter || !chain.IsTestnet || chain.Network() != target.network || !sameNetworkID(chain.NetworkID, target.networkID) {
			return fmt.Errorf("%w: %s seed does not match its actual physical test network", ErrPublicTestUSDTConflict, target.chainID)
		}
	}
	return nil
}
