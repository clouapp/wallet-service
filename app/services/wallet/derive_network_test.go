package wallet

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// networkFlagChain is the chain port with only the testnet flag the wallet
// service reads. Address bytes come from the addressing package.
type networkFlagChain struct {
	*mocks.MockChain
	testnet bool
}

func (c *networkFlagChain) IsTestnet() bool { return c.testnet }

const derivePubKeyHex = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

func TestBtcRecordOnTestnetDerivesTb1Addresses(t *testing.T) {
	pub, err := hex.DecodeString(derivePubKeyHex)
	require.NoError(t, err)

	testnetRegistry := chain.NewRegistry()
	testnetRegistry.RegisterChain(&networkFlagChain{MockChain: mocks.NewMockChain(models.ChainBTC), testnet: true})
	mainnetRegistry := chain.NewRegistry()
	mainnetRegistry.RegisterChain(&networkFlagChain{MockChain: mocks.NewMockChain(models.ChainBTC)})

	onTestnet, err := newTestService(t, testnetRegistry).deriveChainAddress(models.ChainBTC, pub)
	require.NoError(t, err)
	onMainnet, err := newTestService(t, mainnetRegistry).deriveChainAddress(models.ChainBTC, pub)
	require.NoError(t, err)

	assert.Equal(t, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", onTestnet)
	assert.Equal(t, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", onMainnet)
}

func TestAdaptersWithoutANetworkFlagKeepTheChainIDDerivation(t *testing.T) {
	pub, err := hex.DecodeString(derivePubKeyHex)
	require.NoError(t, err)
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain(models.ChainBTC))
	svc := newTestService(t, registry)

	unregistered, err := newTestService(t, chain.NewRegistry()).deriveChainAddress(models.ChainBTC, pub)
	require.NoError(t, err)
	mocked, err := svc.deriveChainAddress(models.ChainBTC, pub)
	require.NoError(t, err)

	assert.Equal(t, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", unregistered)
	assert.Equal(t, unregistered, mocked)
	assert.False(t, (&Service{}).chainIsTestnet(models.ChainBTC), "a service without a registry reads mainnet")
}
