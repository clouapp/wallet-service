package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// derivingChain is a chain adapter that derives each address's token accounts, as
// the Solana adapter does.
type derivingChain struct {
	*mocks.MockChain
	failFor string
}

func (d derivingChain) DerivedAccounts(_ context.Context, owner string) ([]string, error) {
	if owner == d.failFor {
		return nil, errors.New("mint program not read")
	}
	return []string{owner, owner + "-usdc", owner + "-usdt"}, nil
}

func (d derivingChain) AccountStates(context.Context, []string) (chainpkg.AccountStates, error) {
	return chainpkg.AccountStates{}, nil
}

func TestWatched_Accounts_AddTheTokenAccountsOfAChainThatDerivesThem(t *testing.T) {
	registry := chainpkg.NewRegistry()
	registry.RegisterChain(derivingChain{MockChain: mocks.NewMockChain("sol"), failFor: "carol"})
	registry.RegisterChain(mocks.NewMockChain("eth"))
	watched := watchedAccounts(func() (*chainpkg.Registry, error) { return registry, nil })
	ctx := context.Background()

	assert.Equal(t, []string{"alice", "alice-usdc", "alice-usdt", "bob", "bob-usdc", "bob-usdt", "carol"},
		watched(ctx, "sol", []string{"alice", "bob", "carol"}), "carol's accounts were not derived: carol alone")
	assert.Equal(t, []string{"0xabc"}, watched(ctx, "eth", []string{"0xabc"}), "an EVM token is credited to the address itself")
	assert.Equal(t, []string{"x"}, watched(ctx, "unknown", []string{"x"}))
	assert.Equal(t, []string{"alice"}, watchedAccounts(func() (*chainpkg.Registry, error) { return nil, errors.New("boot") })(ctx, "sol", []string{"alice"}))
}
