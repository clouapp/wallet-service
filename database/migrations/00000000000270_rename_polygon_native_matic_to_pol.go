package migrations

import (
	"strings"

	"github.com/goravel/framework/facades"
)

// The symbols as pkg/types spelled them when this migration was written.
const (
	nativeSymbolPOL         = "pol"
	legacyNativeSymbolMATIC = "matic"
)

// M00000000000270RenamePolygonNativeMaticToPol relabels the Polygon native asset
// from the retired MATIC ticker to POL. Only symbols change: addresses, keys,
// amounts and network ids are untouched. Re-running it is a no-op. The migrator
// runs Up inside its own transaction.
type M00000000000270RenamePolygonNativeMaticToPol struct{}

func (r *M00000000000270RenamePolygonNativeMaticToPol) Signature() string {
	return "00000000000270_rename_polygon_native_matic_to_pol"
}

func (r *M00000000000270RenamePolygonNativeMaticToPol) Up() error {
	return renameNativeSymbol(legacyNativeSymbolMATIC, nativeSymbolPOL)
}

func (r *M00000000000270RenamePolygonNativeMaticToPol) Down() error {
	return renameNativeSymbol(nativeSymbolPOL, legacyNativeSymbolMATIC)
}

func renameNativeSymbol(from, to string) error {
	fromCode, toCode := strings.ToUpper(from), strings.ToUpper(to)
	statements := []struct {
		sql  string
		args []any
	}{
		{`UPDATE chains SET native_symbol = ? WHERE adapter_type = 'evm' AND lower(native_symbol) = ?`, []any{to, from}},
		{`UPDATE wallets SET balance_asset = ? WHERE lower(balance_asset) = ?`, []any{to, from}},
		{`DELETE FROM wallet_asset_balances legacy
		   WHERE legacy.asset_type = 'native' AND lower(legacy.asset_symbol) = ?
		     AND EXISTS (SELECT 1 FROM wallet_asset_balances current
		                  WHERE current.wallet_id = legacy.wallet_id AND current.chain_id = legacy.chain_id
		                    AND current.asset_type = 'native' AND lower(current.asset_key) = ?)`, []any{from, to}},
		{`UPDATE wallet_asset_balances
		     SET asset_symbol = ?, asset_key = CASE WHEN lower(asset_key) = ? THEN ? ELSE asset_key END
		   WHERE asset_type = 'native' AND lower(asset_symbol) = ?`, []any{to, from, to, from}},
		{`UPDATE wallet_balance_snapshots SET balance_asset = ? WHERE lower(balance_asset) = ?`, []any{to, from}},
		{`UPDATE transactions SET asset = ? WHERE lower(asset) = ? AND coalesce(token_contract, '') = ''`, []any{to, from}},
		{`UPDATE currencies SET code = ?, symbol = ?
		   WHERE code = ? AND NOT EXISTS (SELECT 1 FROM currencies WHERE code = ?)`, []any{toCode, toCode, fromCode, toCode}},
	}
	for _, statement := range statements {
		if _, err := facades.Orm().Query().Exec(statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return nil
}
