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

func publicTestUSDTContracts() map[string]string {
	return map[string]string{
		models.ChainPolygon:  amoyPublicUSDT,
		models.ChainSOL:      devnetPublicUSDT,
		models.ChainArbitrum: arbPublicUSDT,
	}
}

func TestPublicTestUSDTAlignmentCreatesExactlyThreeAndPreservesExistingRows(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	q := facades.Orm().Query()
	tokens := repositories.NewTokenRepository(nil)

	// Reproduce the previously aligned catalog without the three new test assets.
	for chainID, contract := range publicTestUSDTContracts() {
		row, err := tokens.FindByChainAndContract(ctx, chainID, contract)
		require.NoError(t, err)
		_, err = q.Model(&models.Token{}).Where("id = ?", row.ID).Delete()
		require.NoError(t, err)
	}
	custom := models.Token{ID: uuid.New(), ChainID: models.ChainPolygon, Symbol: "OPERATOR_USDT", Name: "Operator custom token", ContractAddress: "0x00000000000000000000000000000000000000a1", Decimals: 18, Status: models.TokenStatusActive}
	unrelatedNetwork := models.Token{ID: uuid.New(), ChainID: models.ChainETH, Symbol: models.SymbolUSDT, Name: "Unrelated operator deployment", ContractAddress: amoyPublicUSDT, Decimals: 18, Status: models.TokenStatusActive}
	retired := models.Token{ID: uuid.New(), ChainID: models.ChainPolygon, Symbol: models.SymbolUSDT, Name: "Historical invalid Amoy contract", ContractAddress: nonexistentAmoyUSDT, Decimals: 6, Status: models.TokenStatusInactive}
	for _, row := range []*models.Token{&custom, &unrelatedNetwork, &retired} {
		require.NoError(t, q.Create(row))
	}
	var before []models.Token
	require.NoError(t, q.Order("id").Find(&before))
	var aliasesBefore []models.Token
	require.NoError(t, q.Where("chain_id IN ?", []string{models.ChainTPolygon, models.ChainTSOL, models.ChainTArbitrum}).Order("id").Find(&aliasesBefore))

	dry, err := cat.AlignNetworkTokens(ctx, false)
	require.NoError(t, err)
	require.Len(t, dry.Changes, 3)
	for _, change := range dry.Changes {
		require.Equal(t, chaincatalog.TokenActionCreate, change.Action)
		require.Equal(t, models.SymbolUSDT, change.Symbol)
		require.Equal(t, publicTestUSDTContracts()[change.ChainID], change.Contract)
	}
	var afterDryRun []models.Token
	require.NoError(t, q.Order("id").Find(&afterDryRun))
	require.Equal(t, before, afterDryRun, "preview must not write")

	applied, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Equal(t, dry.Changes, applied.Changes)
	active, err := tokens.FindActive(ctx)
	require.NoError(t, err)
	for chainID, contract := range publicTestUSDTContracts() {
		matched := 0
		for _, row := range active {
			if row.ChainID == chainID && row.ContractAddress == contract {
				matched++
				require.Equal(t, models.SymbolUSDT, row.Symbol)
				require.Equal(t, 6, row.Decimals)
				require.NotEqual(t, "Tether USD", row.Name, "test labels must not claim issuer provenance")
			}
		}
		require.Equal(t, 1, matched, "the shared active catalog exposes each canonical test pair exactly once")
	}
	for _, expected := range before {
		var current models.Token
		require.NoError(t, q.Where("id = ?", expected.ID).First(&current))
		require.Equal(t, expected, current, "existing metadata, identity and inactive history remain unchanged")
	}
	var aliasesAfter []models.Token
	require.NoError(t, q.Where("chain_id IN ?", []string{models.ChainTPolygon, models.ChainTSOL, models.ChainTArbitrum}).Order("id").Find(&aliasesAfter))
	require.Equal(t, aliasesBefore, aliasesAfter)
	require.NoError(t, cat.SeedTokens(ctx))
	again, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Empty(t, again.Changes, "reseed and replay must not duplicate new tokens or reactivate retired history")
}

func TestPublicTestUSDTSeedNeverOverwritesExistingOperatorMetadata(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	q := facades.Orm().Query()
	existing := models.Token{ID: uuid.New(), ChainID: models.ChainPolygon, Symbol: "OPERATOR_USDT", Name: "Operator reviewed alias", ContractAddress: amoyPublicUSDT, Decimals: 6, Status: models.TokenStatusActive}
	require.NoError(t, q.Create(&existing))
	var persisted models.Token
	require.NoError(t, q.Where("id = ?", existing.ID).First(&persisted))
	require.NoError(t, cat.SeedTokens(ctx))
	_, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	var current models.Token
	require.NoError(t, q.Where("id = ?", existing.ID).First(&current))
	require.Equal(t, persisted, current, "an existing contract registration is not silently relabeled")
}

func TestPublicTestUSDTAlignmentDoesNotChangeMainnetCustomRows(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, "") // The default production catalog uses mainnet without rewriting stored RPCs.
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	q := facades.Orm().Query()
	for chainID, contract := range publicTestUSDTContracts() {
		require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: chainID, Symbol: models.SymbolUSDT, Name: "Operator token on a different physical network", ContractAddress: contract, Decimals: 18, Status: models.TokenStatusActive}))
	}
	var before []models.Token
	require.NoError(t, q.Order("id").Find(&before))
	report, err := cat.AlignNetworkTokens(ctx, true)
	require.NoError(t, err)
	require.Empty(t, report.Changes)
	var after []models.Token
	require.NoError(t, q.Order("id").Find(&after))
	require.Equal(t, before, after, "test catalog entries cannot activate, relabel or retire anything on mainnet")
}

func removePublicTestUSDTFromFixture(t *testing.T, ctx context.Context) {
	t.Helper()
	for chainID, contract := range publicTestUSDTContracts() {
		row, err := repositories.NewTokenRepository(nil).FindByChainAndContract(ctx, chainID, contract)
		require.NoError(t, err)
		_, err = facades.Orm().Query().Model(&models.Token{}).Where("id = ?", row.ID).Delete()
		require.NoError(t, err)
	}
}

func TestPublicTestUSDTRefusesCustomSymbolCollisionBeforeAnyWrite(t *testing.T) {
	for _, chainID := range []string{models.ChainPolygon, models.ChainSOL, models.ChainArbitrum} {
		t.Run(chainID, func(t *testing.T) {
			fixtures.TestDB(t)
			ctx := context.Background()
			restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
			cat := newCatalog()
			require.NoError(t, cat.SeedChains(ctx))
			require.NoError(t, cat.SeedTokens(ctx))
			removePublicTestUSDTFromFixture(t, ctx)
			q := facades.Orm().Query()
			custom := models.Token{ID: uuid.New(), ChainID: chainID, Symbol: "usdt", Name: "Operator configured asset", ContractAddress: "operator-existing-contract", Decimals: 18, Status: models.TokenStatusActive}
			require.NoError(t, q.Create(&custom))
			var before []models.Token
			require.NoError(t, q.Order("id").Find(&before))
			for _, apply := range []bool{false, true} {
				_, err := cat.AlignNetworkTokens(ctx, apply)
				require.ErrorIs(t, err, chaincatalog.ErrPublicTestUSDTConflict)
			}
			require.ErrorIs(t, cat.SeedTokens(ctx), chaincatalog.ErrPublicTestUSDTConflict)
			var after []models.Token
			require.NoError(t, q.Order("id").Find(&after))
			require.Equal(t, before, after, "all three candidates must be checked before any create, activation or retirement")
		})
	}
}

func TestPublicTestUSDTApplyRechecksCustomRowsInsertedAfterPreview(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	removePublicTestUSDTFromFixture(t, ctx)
	dry, err := cat.AlignNetworkTokens(ctx, false)
	require.NoError(t, err)
	require.Len(t, dry.Changes, 3)
	q := facades.Orm().Query()
	// Arbitrum is the last candidate: earlier candidates cannot commit first.
	require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: models.ChainArbitrum, Symbol: models.SymbolUSDT, Name: "Late operator asset", ContractAddress: "0x00000000000000000000000000000000000000a2", Decimals: 18, Status: models.TokenStatusActive}))
	var before []models.Token
	require.NoError(t, q.Order("id").Find(&before))
	_, err = cat.AlignNetworkTokens(ctx, true)
	require.ErrorIs(t, err, chaincatalog.ErrPublicTestUSDTConflict)
	var after []models.Token
	require.NoError(t, q.Order("id").Find(&after))
	require.Equal(t, before, after)
}

func TestPublicTestUSDTApplyRechecksPhysicalNetworkAfterPreview(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	removePublicTestUSDTFromFixture(t, ctx)
	dry, err := cat.AlignNetworkTokens(ctx, false)
	require.NoError(t, err)
	require.Len(t, dry.Changes, 3)
	q := facades.Orm().Query()
	_, err = q.Model(&models.Chain{}).Where("id = ?", models.ChainArbitrum).Update(map[string]any{"network_id": models.EVMNetworkIDArbitrumMainnet, "is_testnet": false})
	require.NoError(t, err)
	var before []models.Token
	require.NoError(t, q.Order("id").Find(&before))
	_, err = cat.AlignNetworkTokens(ctx, true)
	require.ErrorIs(t, err, chaincatalog.ErrPublicTestUSDTConflict)
	var after []models.Token
	require.NoError(t, q.Order("id").Find(&after))
	require.Equal(t, before, after, "network drift refuses the entire new-asset set")
}

func TestPublicTestUSDTSeedingRefusesRequestedProfileThatDiffersFromActualMainnet(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, "")
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	require.NoError(t, cat.SeedTokens(ctx))
	q := facades.Orm().Query()
	var before []models.Token
	require.NoError(t, q.Order("id").Find(&before))
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	require.ErrorIs(t, cat.SeedTokens(ctx), chaincatalog.ErrPublicTestUSDTConflict)
	var after []models.Token
	require.NoError(t, q.Order("id").Find(&after))
	require.Equal(t, before, after, "a requested profile cannot enroll test tokens onto actual mainnet rows")
}

func TestPublicTestUSDTNewAddedChainCannotBypassFullConflictPreflight(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	restoreChainNetworkProfile(t, models.ChainNetworkProfileTestnet)
	cat := newCatalog()
	require.NoError(t, cat.SeedChains(ctx))
	q := facades.Orm().Query()
	_, err := q.Model(&models.Chain{}).Where("id IN ?", []string{models.ChainArbitrum, models.ChainTArbitrum}).Delete()
	require.NoError(t, err)
	require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: models.ChainPolygon, Symbol: models.SymbolUSDT, Name: "Existing operator USDT", ContractAddress: "0x00000000000000000000000000000000000000a3", Decimals: 18, Status: models.TokenStatusActive}))
	var chainsBefore []models.Chain
	var tokensBefore []models.Token
	require.NoError(t, q.Order("id").Find(&chainsBefore))
	require.NoError(t, q.Order("id").Find(&tokensBefore))
	for _, apply := range []bool{false, true} {
		_, seedErr := cat.SeedMissingAddedChains(ctx, apply)
		require.ErrorIs(t, seedErr, chaincatalog.ErrPublicTestUSDTConflict)
	}
	var chainsAfter []models.Chain
	var tokensAfter []models.Token
	require.NoError(t, q.Order("id").Find(&chainsAfter))
	require.NoError(t, q.Order("id").Find(&tokensAfter))
	require.Equal(t, chainsBefore, chainsAfter, "failed preflight rolls back the newly inserted chain rows too")
	require.Equal(t, tokensBefore, tokensAfter)
}
