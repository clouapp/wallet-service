package chaincatalog

import (
	"context"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// SeedTokens inserts the ERC-20 / SPL / TRC-20 token rows of every chain: the primary
// and added records get the tokens of the network the profile points them at, the
// t-prefixed records their own lists. A token already listed on its chain is left as it
// is.
func (cat *Catalog) SeedTokens(ctx context.Context) error {
	profile, err := cat.profile()
	if err != nil {
		return err
	}
	profile = profileOrMainnet(profile)
	primary, err := tokensForChains(primaryChainSeeds(), profile)
	if err != nil {
		return err
	}
	added, err := cat.addedChainTokens(profile)
	if err != nil {
		return err
	}

	tokens := append(append(append([]tokenSeed(nil), primary...), testRecordTokens...), added...)
	return repositories.NewTokenRepository(nil).WithinCatalog(ctx, func(txCtx context.Context) error {
		if profile == models.ChainNetworkProfileTestnet {
			if preflightErr := cat.preflightPublicTestUSDT(txCtx, true, publicTestFullSeed); preflightErr != nil {
				return preflightErr
			}
		}
		for _, token := range tokens {
			if _, insertErr := insertTokenUnlessPresent(txCtx, repositories.NewTokenRepository(nil), token); insertErr != nil {
				return insertErr
			}
		}
		return nil
	})
}

// profileOrMainnet is the profile that decides where the primary records point; an
// unset profile seeds them as mainnet.
func profileOrMainnet(profile string) string {
	if profile == "" {
		return models.ChainNetworkProfileMainnet
	}
	return profile
}
