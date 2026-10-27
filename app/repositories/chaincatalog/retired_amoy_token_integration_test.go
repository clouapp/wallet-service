package chaincatalog_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/repositories/chaincatalog"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const nonexistentAmoyUSDT = "0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13"

func TestRetireAmoyUSDTOnlyExactRowsPreservingHistoryAndOtherCustomTokens(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	q := facades.Orm().Query()
	tokenRows := repositories.NewTokenRepository(nil)
	ids := map[string]uuid.UUID{}
	for _, chainID := range []string{models.ChainPolygon, models.ChainTPolygon} {
		token := models.Token{ID: uuid.New(), ChainID: chainID, Symbol: models.SymbolUSDT, Name: "Legacy nonexistent test contract", ContractAddress: nonexistentAmoyUSDT, Decimals: 6, Status: "active"}
		require.NoError(t, q.Create(&token))
		ids[chainID] = token.ID
		require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: chainID, Symbol: models.SymbolUSDT, Name: "Operator custom USDT", ContractAddress: "0x00000000000000000000000000000000000000a1", Decimals: 6, Status: "active"}))
	}
	dry, err := cat.AlignNetworkTokens(ctx, false)
	require.NoError(t, err)
	require.Len(t, dry.Changes, 2)
	for _, change := range dry.Changes {
		require.Contains(t, ids, change.ChainID)
		require.Equal(t, models.NetworkPolygonAmoy, change.Network)
		require.Equal(t, models.SymbolUSDT, change.Symbol)
		require.Equal(t, nonexistentAmoyUSDT, change.Contract)
		require.Equal(t, chaincatalog.TokenActionDeactivate, change.Action)
	}
	for chainID := range ids {
		row, readErr := tokenRows.FindByChainAndContract(ctx, chainID, nonexistentAmoyUSDT)
		require.NoError(t, readErr)
		require.Equal(t, "active", row.Status)
	}
	applied, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Len(t, applied.Changes, 2)
	for chainID, id := range ids {
		row, readErr := tokenRows.FindByChainAndContract(ctx, chainID, nonexistentAmoyUSDT)
		require.NoError(t, readErr)
		require.Equal(t, id, row.ID, "retirement must preserve the historical token row")
		require.Equal(t, "inactive", row.Status)
		custom, customErr := tokenRows.FindByChainAndContract(ctx, chainID, "0x00000000000000000000000000000000000000a1")
		require.NoError(t, customErr)
		require.Equal(t, "active", custom.Status)
	}
	require.NoError(t, cat.SeedTokens(ctx))
	again, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Empty(t, again.Changes, "neither reseeding nor rerunning may reactivate the retired record")
}
func TestRetireAmoyUSDTDoesNotTouchSameContractOnAnotherPhysicalNetwork(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, "")
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	q := facades.Orm().Query()
	// Even a t-prefixed row is protected when its actual network is mainnet.
	_, err := q.Model(&models.Chain{}).Where("id = ?", models.ChainTPolygon).Update(map[string]any{"network_id": int64(137), "is_testnet": false})
	require.NoError(t, err)
	for _, chainID := range []string{models.ChainPolygon, models.ChainTPolygon} {
		require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: chainID, Symbol: models.SymbolUSDT, Name: "Custom unrelated network", ContractAddress: nonexistentAmoyUSDT, Decimals: 6, Status: "active"}))
	}
	report, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Empty(t, report.Changes)
	for _, chainID := range []string{models.ChainPolygon, models.ChainTPolygon} {
		row, readErr := repositories.NewTokenRepository(nil).FindByChainAndContract(ctx, chainID, nonexistentAmoyUSDT)
		require.NoError(t, readErr)
		require.Equal(t, "active", row.Status)
	}
}
