package migrations

import (
	"fmt"
	"log/slog"

	"github.com/goravel/framework/facades"
)

// M00000000000280EnforceNonNegativeAmounts makes every stored amount absolute: the
// direction of a movement belongs to its transaction type (deposit, withdrawal,
// sweep, gas seed), never to the sign of the amount.
//
// Up copies each negative value, with its full row, into amount_sign_backups, sets a
// missing transaction direction from tx_type (a negative sign meant "outgoing"),
// strips the sign, and adds one CHECK constraint per amount column. Re-running it
// is a no-op: no negative rows remain and the constraints are dropped and re-added.
//
// Down drops the constraints only. It never writes the negative values back, and
// keeps amount_sign_backups when it holds rows so the original values stay auditable.
type M00000000000280EnforceNonNegativeAmounts struct{}

const amountSignBackupTable = "amount_sign_backups"

type nonNegativeAmountColumn struct {
	table      string
	column     string
	numeric    bool
	constraint string
}

var nonNegativeAmountColumns = []nonNegativeAmountColumn{
	{table: "transactions", column: "amount", constraint: "transactions_amount_non_negative"},
	{table: "transactions", column: "fee", constraint: "transactions_fee_non_negative"},
	{table: "withdrawals", column: "amount", numeric: true, constraint: "withdrawals_amount_non_negative"},
	{table: "withdrawals", column: "fee_estimate", numeric: true, constraint: "withdrawals_fee_estimate_non_negative"},
	{table: "wallet_asset_balances", column: "amount_raw", constraint: "wallet_asset_balances_amount_raw_non_negative"},
	{table: "wallet_asset_balances", column: "amount_display", constraint: "wallet_asset_balances_amount_display_non_negative"},
	{table: "wallet_balance_snapshots", column: "balance_raw", constraint: "wallet_balance_snapshots_balance_raw_non_negative"},
	{table: "wallet_balance_snapshots", column: "balance_display", constraint: "wallet_balance_snapshots_balance_display_non_negative"},
	{table: "wallets", column: "balance_raw", constraint: "wallets_balance_raw_non_negative"},
	{table: "wallets", column: "balance_display", constraint: "wallets_balance_display_non_negative"},
	{table: "wallet_utxos", column: "value_raw", constraint: "wallet_utxos_value_raw_non_negative"},
}

func (r *M00000000000280EnforceNonNegativeAmounts) Signature() string {
	return "00000000000280_enforce_non_negative_amounts"
}

func (c nonNegativeAmountColumn) negativePredicate(alias string) string {
	if c.numeric {
		return fmt.Sprintf("%s.%s < 0", alias, c.column)
	}
	return fmt.Sprintf("btrim(%s.%s) LIKE '-%%'", alias, c.column)
}

func (c nonNegativeAmountColumn) absoluteValue() string {
	if c.numeric {
		return fmt.Sprintf("abs(%s)", c.column)
	}
	return fmt.Sprintf("ltrim(btrim(%s), '-')", c.column)
}

func (c nonNegativeAmountColumn) checkExpression() string {
	if c.numeric {
		return fmt.Sprintf("%s >= 0", c.column)
	}
	return fmt.Sprintf("btrim(%s) NOT LIKE '-%%'", c.column)
}

func (r *M00000000000280EnforceNonNegativeAmounts) Up() error {
	if err := execMigrationSQL(`CREATE TABLE IF NOT EXISTS ` + amountSignBackupTable + ` (
		id             bigserial PRIMARY KEY,
		table_name     varchar(64) NOT NULL,
		row_id         uuid NOT NULL,
		column_name    varchar(64) NOT NULL,
		original_value text NOT NULL,
		row_snapshot   jsonb NOT NULL,
		backed_up_at   timestamptz NOT NULL DEFAULT now(),
		UNIQUE (table_name, row_id, column_name)
	)`); err != nil {
		return err
	}

	for _, target := range nonNegativeAmountColumns {
		if err := execMigrationSQL(fmt.Sprintf(
			`INSERT INTO %[1]s (table_name, row_id, column_name, original_value, row_snapshot)
			 SELECT '%[2]s', t.id, '%[3]s', t.%[3]s::text, to_jsonb(t) FROM %[2]s t WHERE %[4]s
			 ON CONFLICT (table_name, row_id, column_name) DO NOTHING`,
			amountSignBackupTable, target.table, target.column, target.negativePredicate("t"),
		)); err != nil {
			return fmt.Errorf("back up negative %s.%s: %w", target.table, target.column, err)
		}
	}

	if err := execMigrationSQL(`UPDATE transactions t
		SET direction = (CASE
			WHEN t.tx_type = 'deposit' THEN 'inbound'
			WHEN t.tx_type IN ('sweep', 'gas_seed') THEN 'self'
			ELSE 'outbound' END)::transaction_direction
		WHERE (btrim(t.amount) LIKE '-%' OR btrim(t.fee) LIKE '-%')
		  AND (t.direction IS NULL OR t.direction = 'unknown')`); err != nil {
		return fmt.Errorf("set direction of signed transactions: %w", err)
	}

	for _, target := range nonNegativeAmountColumns {
		result, err := facades.Orm().Query().Exec(fmt.Sprintf(
			`UPDATE %[1]s t SET %[2]s = %[3]s WHERE %[4]s`,
			target.table, target.column, target.absoluteValue(), target.negativePredicate("t"),
		))
		if err != nil {
			return fmt.Errorf("normalize %s.%s: %w", target.table, target.column, err)
		}
		if result != nil && result.RowsAffected > 0 {
			slog.Info("normalized negative amounts to absolute values",
				"table", target.table, "column", target.column, "rows", result.RowsAffected, "backup", amountSignBackupTable)
		}
	}

	for _, target := range nonNegativeAmountColumns {
		if err := dropNonNegativeConstraint(target); err != nil {
			return err
		}
		if err := execMigrationSQL(fmt.Sprintf(
			`ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)`,
			target.table, target.constraint, target.checkExpression(),
		)); err != nil {
			return fmt.Errorf("add %s: %w", target.constraint, err)
		}
	}
	return nil
}

func (r *M00000000000280EnforceNonNegativeAmounts) Down() error {
	for _, target := range nonNegativeAmountColumns {
		if err := dropNonNegativeConstraint(target); err != nil {
			return err
		}
	}
	return execMigrationSQL(`DO $$
	DECLARE backup_is_empty boolean;
	BEGIN
		IF to_regclass('` + amountSignBackupTable + `') IS NULL THEN
			RETURN;
		END IF;
		EXECUTE 'SELECT NOT EXISTS (SELECT 1 FROM ` + amountSignBackupTable + `)' INTO backup_is_empty;
		IF backup_is_empty THEN
			EXECUTE 'DROP TABLE ` + amountSignBackupTable + `';
		END IF;
	END $$`)
}

func dropNonNegativeConstraint(target nonNegativeAmountColumn) error {
	if err := execMigrationSQL(fmt.Sprintf(
		`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s`, target.table, target.constraint,
	)); err != nil {
		return fmt.Errorf("drop %s: %w", target.constraint, err)
	}
	return nil
}

func execMigrationSQL(statement string) error {
	_, err := facades.Orm().Query().Exec(statement)
	return err
}
