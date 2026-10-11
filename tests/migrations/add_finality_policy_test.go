package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func finalityColumns(t *testing.T) int64 {
	t.Helper()
	return countRows(t, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name IN ('chains', 'transactions')
		  AND column_name = 'finality' AND is_nullable = 'NO'
		  AND column_default LIKE '''confirmations''%'`)
}

func insertChainWithConfirmations(t *testing.T, id, adapterType string, confirmations int) {
	t.Helper()
	exec(t, `INSERT INTO chains (id, name, adapter_type, native_symbol, native_decimals, rpc_url, required_confirmations, status)
		VALUES (?, ?, ?, 'native', 18, 'rpc', ?, 'active')`, id, id, adapterType, confirmations)
}

func chainFinality(t *testing.T, id string) (string, int) {
	t.Helper()
	var row struct {
		Finality              string
		RequiredConfirmations int
	}
	require.NoError(t, facades.Orm().Query().Raw(
		`SELECT finality, required_confirmations FROM chains WHERE id = ?`, id,
	).Scan(&row))
	return row.Finality, row.RequiredConfirmations
}

func transactionFinality(t *testing.T, id uuid.UUID) (string, int) {
	t.Helper()
	var row struct {
		Finality      string
		RequiredConfs int
	}
	require.NoError(t, facades.Orm().Query().Raw(
		`SELECT finality, required_confs FROM transactions WHERE id = ?`, id,
	).Scan(&row))
	return row.Finality, row.RequiredConfs
}

func TestAdd_FinalityPolicy_MovesNativeFinalityChainsAndLeavesTheOthers(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000640AddFinalityPolicy{}
	assert.Equal(t, int64(2), finalityColumns(t))

	require.NoError(t, migration.Down())
	assert.Zero(t, finalityColumns(t))
	// The rows as the earlier seed left them.
	for _, row := range []struct {
		id, adapter   string
		confirmations int
	}{
		{models.ChainPolygon, models.AdapterTypeEVM, 128},
		{models.ChainTPolygon, models.AdapterTypeEVM, 128},
		{models.ChainBSC, models.AdapterTypeEVM, 30},
		{models.ChainTBSC, models.AdapterTypeEVM, 30},
		{models.ChainTron, models.AdapterTypeTron, 20},
		{models.ChainTTron, models.AdapterTypeTron, 20},
		{models.ChainETH, models.AdapterTypeEVM, 12},
		{models.ChainBase, models.AdapterTypeEVM, 12},
		{models.ChainArbitrum, models.AdapterTypeEVM, 120},
		{models.ChainBTC, models.AdapterTypeBitcoin, 6},
	} {
		insertChainWithConfirmations(t, row.id, row.adapter, row.confirmations)
	}
	wallet := fixtures.InsertWallet(t, models.ChainPolygon)
	recorded := uuid.New()
	exec(t, `
		INSERT INTO transactions (id, wallet_id, external_user_id, chain, tx_type, to_address, amount, asset, status, required_confs, block_number, created_at, updated_at)
		VALUES (?, ?, 'user', 'polygon', 'deposit', '0xto', '1', 'pol', 'confirming', 128, 100, NOW(), NOW())`,
		recorded, wallet.ID)

	require.NoError(t, migration.Up())
	require.NoError(t, migration.Up(), "Up must be repeatable")
	assert.Equal(t, int64(2), finalityColumns(t))

	for id, want := range map[string]struct {
		finality      string
		confirmations int
	}{
		models.ChainPolygon:  {models.FinalityFinalizedTag, 3},
		models.ChainTPolygon: {models.FinalityFinalizedTag, 3},
		models.ChainBSC:      {models.FinalityFinalizedTag, 2},
		models.ChainTBSC:     {models.FinalityFinalizedTag, 2},
		models.ChainTron:     {models.FinalitySolidified, 19},
		models.ChainTTron:    {models.FinalitySolidified, 19},
		models.ChainETH:      {models.FinalityConfirmations, 12},
		models.ChainBase:     {models.FinalityConfirmations, 12},
		models.ChainArbitrum: {models.FinalityConfirmations, 120},
		models.ChainBTC:      {models.FinalityConfirmations, 6},
	} {
		finality, confirmations := chainFinality(t, id)
		assert.Equal(t, want.finality, finality, id)
		assert.Equal(t, want.confirmations, confirmations, id)
	}
	finality, requiredConfs := transactionFinality(t, recorded)
	assert.Equal(t, models.FinalityConfirmations, finality, "a transaction recorded before keeps counting confirmations")
	assert.Equal(t, 128, requiredConfs)
}

func TestAdd_FinalityPolicy_RefusesAPolicyTheAdapterCannotRead(t *testing.T) {
	fixtures.TestDB(t)
	require.NoError(t, (&migrations.M00000000000640AddFinalityPolicy{}).Up())
	insertChainWithConfirmations(t, models.ChainETH, models.AdapterTypeEVM, 12)
	insertChainWithConfirmations(t, models.ChainTron, models.AdapterTypeTron, 19)
	insertChainWithConfirmations(t, models.ChainBTC, models.AdapterTypeBitcoin, 6)
	wallet := fixtures.InsertWallet(t, models.ChainETH)
	recorded := fixtures.InsertTransaction(t, wallet.ID, nil, models.ChainETH, models.TxTypeDeposit, "pending", "eth", "1", 100)

	update := func(statement string, args ...any) error {
		_, err := facades.Orm().Query().Exec(statement, args...)
		return err
	}
	require.NoError(t, update(`UPDATE chains SET finality = ? WHERE id = ?`, models.FinalityFinalizedTag, models.ChainETH))
	require.NoError(t, update(`UPDATE chains SET finality = ? WHERE id = ?`, models.FinalitySolidified, models.ChainTron))
	assert.Error(t, update(`UPDATE chains SET finality = ? WHERE id = ?`, models.FinalitySolidified, models.ChainETH))
	assert.Error(t, update(`UPDATE chains SET finality = ? WHERE id = ?`, models.FinalityFinalizedTag, models.ChainTron))
	assert.Error(t, update(`UPDATE chains SET finality = ? WHERE id = ?`, models.FinalityFinalizedTag, models.ChainBTC))
	assert.Error(t, update(`UPDATE chains SET finality = 'fast' WHERE id = ?`, models.ChainBTC))
	require.NoError(t, update(`UPDATE transactions SET finality = ? WHERE id = ?`, models.FinalitySolidified, recorded.ID))
	assert.Error(t, update(`UPDATE transactions SET finality = 'fast' WHERE id = ?`, recorded.ID))
}

func TestAdd_FinalityPolicy_DownRestoresTheCountsItLowered(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000640AddFinalityPolicy{}
	require.NoError(t, migration.Down())
	insertChainWithConfirmations(t, models.ChainPolygon, models.AdapterTypeEVM, 128)
	insertChainWithConfirmations(t, models.ChainBSC, models.AdapterTypeEVM, 30)
	insertChainWithConfirmations(t, models.ChainTron, models.AdapterTypeTron, 20)
	insertChainWithConfirmations(t, models.ChainETH, models.AdapterTypeEVM, 12)
	require.NoError(t, migration.Up())

	wallet := fixtures.InsertWallet(t, models.ChainPolygon)
	open := fixtures.InsertTransaction(t, wallet.ID, nil, models.ChainPolygon, models.TxTypeDeposit, "confirming", "pol", "1", 100)
	done := fixtures.InsertTransaction(t, wallet.ID, nil, models.ChainPolygon, models.TxTypeDeposit, "confirmed", "pol", "1", 90)
	counted := fixtures.InsertTransaction(t, wallet.ID, nil, models.ChainETH, models.TxTypeDeposit, "confirming", "eth", "1", 100)
	exec(t, `UPDATE transactions SET finality = ?, required_confs = 3 WHERE id IN (?, ?)`, models.FinalityFinalizedTag, open.ID, done.ID)

	require.NoError(t, migration.Down())
	assert.Zero(t, finalityColumns(t))
	for id, want := range map[string]int{models.ChainPolygon: 128, models.ChainBSC: 30, models.ChainTron: 20, models.ChainETH: 12} {
		assert.Equal(t, want, scalar[int](t, `SELECT required_confirmations FROM chains WHERE id = ?`, id), id)
	}
	requiredConfs := func(id uuid.UUID) int {
		return scalar[int](t, `SELECT required_confs FROM transactions WHERE id = ?`, id)
	}
	assert.Equal(t, 128, requiredConfs(open.ID), "an open transaction recorded under finality counts the restored confirmations")
	assert.Equal(t, 3, requiredConfs(done.ID), "a confirmed transaction is history")
	assert.Equal(t, counted.RequiredConfs, requiredConfs(counted.ID))

	require.NoError(t, migration.Up())
	finality, confirmations := chainFinality(t, models.ChainPolygon)
	assert.Equal(t, models.FinalityFinalizedTag, finality)
	assert.Equal(t, 3, confirmations)
}
