package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func tokenRetirementFixture(t *testing.T) (*repositories.TokenRepository, *models.Token) {
	t.Helper()
	fixtures.TestDB(t)
	network := models.EVMNetworkIDPolygonAmoy
	for _, id := range []string{models.ChainPolygon, models.ChainTPolygon} {
		require.NoError(t, facades.Orm().Query().Create(&models.Chain{ID: id, Name: "Amoy", AdapterType: models.AdapterTypeEVM, NativeSymbol: "pol", NativeDecimals: 18, NetworkID: &network, IsTestnet: true, Status: "active", RpcURL: "https://example.invalid"}))
	}
	token := &models.Token{ID: uuid.New(), ChainID: models.ChainPolygon, Symbol: models.SymbolUSDT, Name: "Legacy invalid", ContractAddress: repositories.InvalidAmoyUSDTContract, Decimals: 6, Status: models.TokenStatusActive}
	repo := repositories.NewTokenRepository(nil)
	require.NoError(t, repo.Create(context.Background(), token))
	return repo, token
}
func TestTokenRetirementRejectsDriftSincePreview(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
		patch map[string]any
	}{
		{"contract", "tokens", map[string]any{"contract_address": "0x00000000000000000000000000000000000000a1"}},
		{"symbol", "tokens", map[string]any{"symbol": "CUSTOM"}},
		{"chain", "tokens", map[string]any{"chain_id": models.ChainTPolygon}},
		{"status", "tokens", map[string]any{"status": models.TokenStatusInactive}},
		{"physical network", "chains", map[string]any{"network_id": int64(137), "is_testnet": false}},
		{"testnet flag", "chains", map[string]any{"is_testnet": false}},
		{"adapter", "chains", map[string]any{"adapter_type": models.AdapterTypeSolana}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, preview := tokenRetirementFixture(t)
			q := facades.Orm().Query()
			if tc.table == "tokens" {
				_, err := q.Model(&models.Token{}).Where("id = ?", preview.ID).Update(tc.patch)
				require.NoError(t, err)
			} else {
				_, err := q.Model(&models.Chain{}).Where("id = ?", preview.ChainID).Update(tc.patch)
				require.NoError(t, err)
			}
			before, err := repo.FindByID(context.Background(), preview.ID)
			require.NoError(t, err)
			err = repo.RetireInvalidAmoyUSDT(context.Background(), preview.ID, preview.ChainID, preview.ContractAddress)
			require.ErrorIs(t, err, repositories.ErrTokenRetirementStale)
			after, err := repo.FindByID(context.Background(), preview.ID)
			require.NoError(t, err)
			require.Equal(t, before.ID, after.ID)
			require.Equal(t, before.ChainID, after.ChainID)
			require.Equal(t, before.Symbol, after.Symbol)
			require.Equal(t, before.ContractAddress, after.ContractAddress)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, before.UpdatedAt, after.UpdatedAt, "refusal must not touch the changed custom row")
		})
	}
}
func TestTokenRetirementBoundUpdateChangesExactlyOneActiveRow(t *testing.T) {
	repo, preview := tokenRetirementFixture(t)
	other := *preview
	other.ID = uuid.New()
	other.ContractAddress = "0x00000000000000000000000000000000000000a1"
	require.NoError(t, repo.Create(context.Background(), &other))
	require.NoError(t, repo.RetireInvalidAmoyUSDT(context.Background(), preview.ID, preview.ChainID, preview.ContractAddress))
	saved, err := repo.FindByID(context.Background(), preview.ID)
	require.NoError(t, err)
	require.Equal(t, preview.ID, saved.ID)
	require.Equal(t, models.TokenStatusInactive, saved.Status)
	untouched, err := repo.FindByID(context.Background(), other.ID)
	require.NoError(t, err)
	require.Equal(t, models.TokenStatusActive, untouched.Status)
	require.ErrorIs(t, repo.RetireInvalidAmoyUSDT(context.Background(), preview.ID, preview.ChainID, preview.ContractAddress), repositories.ErrTokenRetirementStale, "stale preview cannot be applied again; a fresh preview is idempotent")
}
