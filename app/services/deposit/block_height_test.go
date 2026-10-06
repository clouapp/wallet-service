package deposit

import (
	"context"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// evmTipChain is the EVM port the deposit service asks for a tip. EstimateL1DataFee
// marks it as EVM so the block-height router runs; GetLatestBlock is the fallback.
type evmTipChain struct {
	*mocks.MockChain
	height uint64
	reads  int
}

func (c *evmTipChain) EstimateL1DataFee(context.Context, types.TransferRequest) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (c *evmTipChain) GetLatestBlock(context.Context) (uint64, error) {
	c.reads++
	return c.height, nil
}

type failingTipProvider struct{ asked int }

func (p *failingTipProvider) GetBlockHeight(context.Context, string) (uint64, error) {
	p.asked++
	return 0, context.DeadlineExceeded
}

func TestChain_RPC_NetworksReadTheTipFromTheAdapterOnTheFirstTick(t *testing.T) {
	adapter := &evmTipChain{MockChain: mocks.NewMockChain(models.ChainBase), height: 0x2d6f3c7}
	inner := &failingTipProvider{}
	svc := &Service{
		blockHeightProviders: map[string]blockheight.Provider{
			models.AdapterTypeEVM: blockheight.RouteByNetwork(inner, map[string]string{models.ChainBase: models.NetworkBaseSepolia}),
		},
		heightFailures: make(map[string]int),
	}

	height, ok := svc.resolveCurrentBlockHeight(context.Background(), models.ChainBase, adapter)
	if !ok || height != 0x2d6f3c7 {
		t.Fatalf("height %d ok=%t, want the chain RPC head without waiting for provider failures", height, ok)
	}
	if inner.asked != 0 || adapter.reads != 1 || svc.heightFailures[models.ChainBase] != 0 {
		t.Fatalf("the provider must not be asked (asked %d, adapter reads %d, failures %d)", inner.asked, adapter.reads, svc.heightFailures[models.ChainBase])
	}
}

func TestProvider_Networks_StillWaitForRepeatedFailuresBeforeFallingBack(t *testing.T) {
	adapter := &evmTipChain{MockChain: mocks.NewMockChain(models.ChainETH), height: 0x10}
	inner := &failingTipProvider{}
	svc := &Service{
		blockHeightProviders: map[string]blockheight.Provider{
			models.AdapterTypeEVM: blockheight.RouteByNetwork(inner, map[string]string{models.ChainETH: models.NetworkEthereumSepolia}),
		},
		heightFailures: make(map[string]int),
	}

	if _, ok := svc.resolveCurrentBlockHeight(context.Background(), models.ChainETH, adapter); ok {
		t.Fatal("a first provider failure on a provider-served network must not fall back yet")
	}
	if inner.asked != 1 || adapter.reads != 0 {
		t.Fatalf("provider asked %d times and the adapter was read %d times; want one provider failure and no adapter read", inner.asked, adapter.reads)
	}
}
