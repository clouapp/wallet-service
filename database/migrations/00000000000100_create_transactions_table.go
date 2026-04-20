package migrations

import "github.com/goravel/framework/facades"

type M00000000000100CreateTransactionsTable struct{}

func (r *M00000000000100CreateTransactionsTable) Signature() string {
	return "00000000000100_create_transactions_table"
}

func (r *M00000000000100CreateTransactionsTable) Up() error {
	stmts := []string{
		`CREATE TABLE transactions (
			id                    UUID         NOT NULL,
			address_id            UUID,
			wallet_id             UUID         NOT NULL,
			external_user_id      VARCHAR(255) NOT NULL,
			chain                 VARCHAR(50)  NOT NULL,
			tx_type               transaction_type   NOT NULL,
			type                  VARCHAR(20),
			tx_hash               VARCHAR(255),
			from_address          VARCHAR(255),
			to_address            VARCHAR(255) NOT NULL,
			amount                VARCHAR(100) NOT NULL,
			asset                 VARCHAR(50)  NOT NULL,
			token_contract        VARCHAR(255),
			confirmations         INTEGER      NOT NULL DEFAULT 0,
			required_confs        INTEGER      NOT NULL DEFAULT 12,
			status                transaction_status NOT NULL,
			fee                   VARCHAR(100),
			block_number          BIGINT,
			block_hash            VARCHAR(255),
			error_message         TEXT,
			idempotency_key       VARCHAR(255),
			log_index             INTEGER      NOT NULL DEFAULT -1,
			confirmed_at          TIMESTAMPTZ,
			created_at            TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at            TIMESTAMP(0) WITHOUT TIME ZONE,
			direction             transaction_direction,
			source                transaction_source,
			raw_payload           JSONB,
			synced_at             TIMESTAMPTZ,
			parent_transaction_id UUID,
			origin                VARCHAR(24),
			CONSTRAINT transactions_pkey                PRIMARY KEY (id),
			CONSTRAINT transactions_address_id_foreign  FOREIGN KEY (address_id) REFERENCES addresses(id),
			CONSTRAINT transactions_wallet_id_foreign   FOREIGN KEY (wallet_id)  REFERENCES wallets(id)
		)`,
		`CREATE INDEX transactions_address_id_index            ON transactions (address_id)`,
		`CREATE INDEX transactions_wallet_id_index             ON transactions (wallet_id)`,
		`CREATE INDEX transactions_external_user_id_index      ON transactions (external_user_id)`,
		`CREATE INDEX transactions_tx_type_index               ON transactions (tx_type)`,
		`CREATE INDEX transactions_status_index                ON transactions (status)`,
		`CREATE INDEX transactions_chain_tx_hash_index         ON transactions (chain, tx_hash)`,
		`CREATE INDEX idx_tx_dedup                             ON transactions (chain, tx_hash, log_index, tx_type)`,
		`CREATE INDEX transactions_chain_status_index          ON transactions (chain, status)`,
		`CREATE INDEX transactions_created_at_index            ON transactions (created_at)`,
		`CREATE INDEX transactions_parent_transaction_id_index ON transactions (parent_transaction_id)`,
		`CREATE INDEX transactions_origin_index                ON transactions (origin)`,
		`CREATE UNIQUE INDEX transactions_idempotency_key_unique ON transactions (idempotency_key) WHERE idempotency_key IS NOT NULL`,
		`COMMENT ON TABLE  transactions                       IS 'Transaction history for deposits and withdrawals'`,
		`COMMENT ON COLUMN transactions.address_id            IS 'Deposit address (nullable for withdrawals)'`,
		`COMMENT ON COLUMN transactions.wallet_id             IS 'Foreign key to wallets table'`,
		`COMMENT ON COLUMN transactions.external_user_id      IS 'Client''s user identifier'`,
		`COMMENT ON COLUMN transactions.chain                 IS 'Blockchain identifier'`,
		`COMMENT ON COLUMN transactions.tx_type               IS 'deposit or withdrawal'`,
		`COMMENT ON COLUMN transactions.type                  IS 'Transaction type for filtering'`,
		`COMMENT ON COLUMN transactions.tx_hash               IS 'Blockchain transaction hash'`,
		`COMMENT ON COLUMN transactions.from_address          IS 'Source address'`,
		`COMMENT ON COLUMN transactions.to_address            IS 'Destination address'`,
		`COMMENT ON COLUMN transactions.amount                IS 'Amount (stored as string for precision)'`,
		`COMMENT ON COLUMN transactions.asset                 IS 'Asset symbol (ETH, USDC, SOL, BTC, etc.)'`,
		`COMMENT ON COLUMN transactions.token_contract        IS 'ERC20/SPL token contract address'`,
		`COMMENT ON COLUMN transactions.confirmations         IS 'Current confirmation count'`,
		`COMMENT ON COLUMN transactions.required_confs        IS 'Required confirmations for finality'`,
		`COMMENT ON COLUMN transactions.status                IS 'pending, confirmed, failed'`,
		`COMMENT ON COLUMN transactions.fee                   IS 'Transaction fee (string for precision)'`,
		`COMMENT ON COLUMN transactions.block_number          IS 'Block number'`,
		`COMMENT ON COLUMN transactions.block_hash            IS 'Block hash'`,
		`COMMENT ON COLUMN transactions.error_message         IS 'Error details if failed'`,
		`COMMENT ON COLUMN transactions.idempotency_key       IS 'Prevents duplicate withdrawals'`,
		`COMMENT ON COLUMN transactions.log_index             IS 'EVM log index for deposit dedup (-1 when N/A)'`,
		`COMMENT ON COLUMN transactions.confirmed_at          IS 'Timestamp when tx reached required confirmations'`,
		`COMMENT ON COLUMN transactions.parent_transaction_id IS 'Withdrawal tx id that triggered this sweep/gas_seed'`,
		`COMMENT ON COLUMN transactions.origin                IS 'user_request | sweep | gas_seed | manual_consolidation'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000100CreateTransactionsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS transactions`)
	return err
}
