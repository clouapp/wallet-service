package chaincatalog

import (
	"context"

	"github.com/macrowallets/waas/app/models"
)

// SeedTokens inserts the ERC-20 / SPL / TRC-20 token rows of every chain: the primary
// and added records get the tokens of the network the profile points them at, the
// t-prefixed records their own lists. A token already listed on its chain is left as it
// is.
func (cat *Catalog) SeedTokens(_ context.Context) error {
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
	for _, t := range tokens {
		if _, err := cat.createTokenUnlessPresent(t); err != nil {
			return err
		}
	}
	return nil
}

// profileOrMainnet is the profile that decides where the primary records point; an
// unset profile seeds them as mainnet.
func profileOrMainnet(profile string) string {
	if profile == "" {
		return models.ChainNetworkProfileMainnet
	}
	return profile
}
