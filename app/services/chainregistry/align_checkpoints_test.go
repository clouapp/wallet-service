package chainregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// fakeCheckpoints is the deposit scanner's checkpoint store; each drop records how
// many chain writes the alignment had made by then.
type fakeCheckpoints struct {
	store        *fakeStore
	blocks       map[string]uint64
	dropErr      map[string]error
	dropped      []string
	writesAtDrop []int
}

func (f *fakeCheckpoints) Checkpoint(_ context.Context, chainID string) (uint64, bool, error) {
	block, found := f.blocks[chainID]
	return block, found, nil
}

func (f *fakeCheckpoints) DropCheckpoint(_ context.Context, chainID string) (uint64, bool, error) {
	if err := f.dropErr[chainID]; err != nil {
		return 0, false, err
	}
	f.dropped = append(f.dropped, chainID)
	f.writesAtDrop = append(f.writesAtDrop, f.store.chainWrite)
	block, found := f.blocks[chainID]
	delete(f.blocks, chainID)
	return block, found, nil
}

// alignerOnVaultTestRegistry moves eth (mainnet record over a Sepolia RPC) and btc
// (mainnet record over a testnet RPC) to other networks under the testnet profile;
// polygon and sol only change environment list.
func alignerOnVaultTestRegistry() (*Aligner, *fakeStore, *fakeCheckpoints) {
	store := vaultTestRegistry()
	checkpoints := &fakeCheckpoints{store: store, blocks: map[string]uint64{
		models.ChainETH:     9_412_000,
		models.ChainBTC:     5_158_070,
		models.ChainPolygon: 27_000_000,
		models.ChainSOL:     410_000_000,
	}, dropErr: map[string]error{}}
	aligner := NewAligner(AlignerDeps{Store: store, Decrypt: plainDecrypt, Checkpoints: checkpoints})
	return aligner, store, checkpoints
}

func infoContaining(report Report, fragment string) []string {
	var lines []string
	for _, line := range report.Info {
		if strings.Contains(line, fragment) {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestAlign_DryRun_ReportsTheCheckpointOfEachChainMovedToAnotherNetwork(t *testing.T) {
	aligner, store, checkpoints := alignerOnVaultTestRegistry()

	report, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", false)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"chain eth: deposit scan checkpoint 9412000 would be dropped; the next scan starts at the head of " + models.NetworkEthereumSepolia,
		"chain btc: deposit scan checkpoint 5158070 would be dropped; the next scan starts at the head of " + models.NetworkBitcoinTestnet,
	}, infoContaining(report, "deposit scan checkpoint"), "only the chains whose network changes lose their checkpoint")
	assert.Contains(t, report.Info, "dry run: nothing written (pass --apply)")
	assert.Empty(t, checkpoints.dropped)
	assert.Zero(t, store.chainWrite)
}

func TestAlign_Apply_DropsTheCheckpointsBeforeMovingTheChains(t *testing.T) {
	aligner, store, checkpoints := alignerOnVaultTestRegistry()

	report, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)

	require.NoError(t, err)
	assert.Equal(t, []string{models.ChainETH, models.ChainBTC}, checkpoints.dropped)
	assert.Equal(t, []int{0, 0}, checkpoints.writesAtDrop, "a checkpoint is dropped before any chain moves")
	assert.Equal(t, 4, store.chainWrite)
	assert.Contains(t, checkpoints.blocks, models.ChainPolygon, "an environment-only change keeps the checkpoint")
	assert.Contains(t, checkpoints.blocks, models.ChainSOL)
	dropped := infoContaining(report, "dropped")
	require.Len(t, dropped, 2)
	assert.Contains(t, dropped[1], "chain btc: deposit scan checkpoint 5158070 dropped; the next scan starts at the head of "+models.NetworkBitcoinTestnet)
	assert.Contains(t, dropped[1], "scan:deposits btc --set-checkpoint=5158070 --apply")

	again, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)
	require.NoError(t, err)
	assert.Len(t, checkpoints.dropped, 2, "an aligned registry drops nothing")
	assert.Empty(t, infoContaining(again, "deposit scan checkpoint"))
}

func TestAlign_Apply_StopsBeforeMovingAnyChainWhenACheckpointCannotBeDropped(t *testing.T) {
	aligner, store, checkpoints := alignerOnVaultTestRegistry()
	scanRunning := errors.New("deposit: another scan of this chain is running")
	checkpoints.dropErr[models.ChainBTC] = scanRunning

	_, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)

	require.ErrorIs(t, err, scanRunning)
	assert.Contains(t, err.Error(), "run the command again")
	assert.Zero(t, store.chainWrite, "no chain may move while its checkpoint survives")

	// Once the scan ends, running the command again plans the same moves and finishes.
	delete(checkpoints.dropErr, models.ChainBTC)
	_, err = aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ChainETH, models.ChainETH, models.ChainBTC}, checkpoints.dropped)
	assert.Equal(t, 4, store.chainWrite)
}

func TestAlign_Apply_WithoutADepositServiceWarnsAndMovesTheChains(t *testing.T) {
	store := vaultTestRegistry()
	aligner := NewAligner(AlignerDeps{Store: store, Decrypt: plainDecrypt})

	report, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)

	require.NoError(t, err)
	assert.Equal(t, 4, store.chainWrite)
	require.Len(t, report.Warning, 2)
	assert.Contains(t, report.Warning[1], "deposit scan checkpoint of btc not dropped")
	assert.Contains(t, report.Warning[1], "scan:deposits btc --set-checkpoint=<head> --apply")
}

// fakeFallbacks is the fallback list of the chains an alignment moves.
type fakeFallbacks struct {
	store    *fakeStore
	rows     map[string][]models.ChainRPCEndpoint
	disabled map[string][]int
	writesAt []int
	err      error
}

func (f *fakeFallbacks) ListByChain(_ context.Context, chainID string) ([]models.ChainRPCEndpoint, error) {
	return f.rows[chainID], nil
}

func (f *fakeFallbacks) DisablePositions(_ context.Context, chainID string, positions []int) error {
	if f.err != nil {
		return f.err
	}
	f.disabled[chainID] = append(f.disabled[chainID], positions...)
	f.writesAt = append(f.writesAt, f.store.chainWrite)
	return nil
}

// sepoliaOnlyVerify says https://sepolia.example serves Sepolia and anything else mainnet.
func sepoliaOnlyVerify(_ context.Context, _ string, target EndpointTarget, rawURL string) (string, error) {
	served := models.EVMNetworkIDEthereumMainnet
	if rawURL == "https://sepolia.example" {
		served = models.EVMNetworkIDEthereumSepolia
	}
	if target.EVMNetworkID != served {
		return models.EVMNetworkName(served), fmt.Errorf("%w: eth_chainId is %d", chain.ErrWrongNetwork, served)
	}
	return models.EVMNetworkName(served), nil
}

func TestAlign_Apply_DisablesTheFallbacksThatDoNotServeTheNewNetwork(t *testing.T) {
	aligner, store, _ := alignerOnVaultTestRegistry()
	fallbacks := &fakeFallbacks{store: store, disabled: map[string][]int{}, rows: map[string][]models.ChainRPCEndpoint{
		models.ChainETH: {
			{ChainID: models.ChainETH, URL: "https://sepolia.example", Position: 2, Enabled: true},
			{ChainID: models.ChainETH, URL: "https://mainnet.example", Position: 3, Enabled: true},
			{ChainID: models.ChainETH, URL: "https://mainnet-off.example", Position: 4, Enabled: false},
		},
		models.ChainPolygon: {{ChainID: models.ChainPolygon, URL: "https://mainnet.example", Position: 2, Enabled: true}},
	}}
	aligner.fallbacks, aligner.verify = fallbacks, sepoliaOnlyVerify

	dry, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"chain eth: fallback 3 mainnet.example would be disabled: it does not serve " + models.NetworkEthereumSepolia},
		infoContaining(dry, "fallback"))
	assert.Empty(t, fallbacks.disabled)

	_, err = aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)
	require.NoError(t, err)
	assert.Equal(t, map[string][]int{models.ChainETH: {3}}, fallbacks.disabled,
		"only the moved chain, only its enabled fallback on the old network; polygon only changes environment")
	assert.Equal(t, []int{0}, fallbacks.writesAt, "fallbacks are disabled before any chain moves")
}

func TestAlign_Apply_StopsBeforeMovingAnyChainWhenAFallbackCannotBeDisabled(t *testing.T) {
	aligner, store, _ := alignerOnVaultTestRegistry()
	fallbacks := &fakeFallbacks{store: store, disabled: map[string][]int{}, err: errors.New("database down"),
		rows: map[string][]models.ChainRPCEndpoint{models.ChainETH: {{ChainID: models.ChainETH, URL: "https://mainnet.example", Position: 2, Enabled: true}}}}
	aligner.fallbacks, aligner.verify = fallbacks, sepoliaOnlyVerify

	_, err := aligner.Align(context.Background(), models.ChainNetworkProfileTestnet, "", true)
	require.Error(t, err)
	assert.Zero(t, store.chainWrite)
}
