package migrations

// M00000000000640AddFinalityPolicy adds the finality policy the confirmation
// tracker confirms by: chains.finality (the policy of new transactions) and
// transactions.finality (the policy a row was recorded under, like required_confs,
// so changing a chain never re-reads an open transaction against a count it was not
// recorded with). Both default to 'confirmations', today's rule.
//
// finalized_tag (EVM, eth_getBlockByNumber "finalized") is valid only on an evm
// chain and solidified (TRON, /walletsolidity) only on a tron chain. Polygon and
// BSC (and their testnets) move to finalized_tag, TRON to solidified; their
// required_confirmations becomes the finality lag measured on 2026-10-11 plus the
// tip block (Amoy 1-2 blocks behind the tip: 3; BSC testnet 1: 2; Nile 18: 19), so
// "n/N confirmations" stays meaningful while the policy decides the status.
// Transactions already recorded keep counting confirmations. The values are copied
// here, not imported: a migration does not change with the app code.
//
// Down puts back the confirmation counts the seed had for those six chains (128,
// 30, 20, never lowering a count), raises the open transactions recorded under a
// finality policy to their chain's count, so none confirms early once the policy is
// gone, and drops the columns.
type M00000000000640AddFinalityPolicy struct{}

func (r *M00000000000640AddFinalityPolicy) Signature() string {
	return "00000000000640_add_finality_policy"
}

func (r *M00000000000640AddFinalityPolicy) Up() error {
	return execMigrationSQL(`
		ALTER TABLE chains ADD COLUMN IF NOT EXISTS finality VARCHAR(20) NOT NULL DEFAULT 'confirmations';
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_finality_fits_adapter;
		ALTER TABLE chains ADD CONSTRAINT chains_finality_fits_adapter CHECK (
			finality = 'confirmations'
			OR (finality = 'finalized_tag' AND adapter_type = 'evm')
			OR (finality = 'solidified' AND adapter_type = 'tron')
		);

		UPDATE chains AS c SET
			finality = v.finality,
			required_confirmations = v.confirmations,
			updated_at = NOW()
		FROM (VALUES
			('polygon'::varchar, 'evm'::varchar, 'finalized_tag'::varchar, 3),
			('tpolygon', 'evm', 'finalized_tag', 3),
			('bsc', 'evm', 'finalized_tag', 2),
			('tbsc', 'evm', 'finalized_tag', 2),
			('tron', 'tron', 'solidified', 19),
			('ttron', 'tron', 'solidified', 19)
		) AS v(id, adapter_type, finality, confirmations)
		WHERE c.id = v.id AND c.adapter_type = v.adapter_type AND c.finality = 'confirmations';

		ALTER TABLE transactions ADD COLUMN IF NOT EXISTS finality VARCHAR(20) NOT NULL DEFAULT 'confirmations';
		ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_finality_valid;
		ALTER TABLE transactions ADD CONSTRAINT transactions_finality_valid
			CHECK (finality IN ('confirmations', 'finalized_tag', 'solidified'));
	`)
}

func (r *M00000000000640AddFinalityPolicy) Down() error {
	return execMigrationSQL(`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'chains' AND column_name = 'finality'
		) THEN
			UPDATE chains AS c SET
				required_confirmations = GREATEST(c.required_confirmations, v.confirmations),
				updated_at = NOW()
			FROM (VALUES
				('polygon'::varchar, 128),
				('tpolygon', 128),
				('bsc', 30),
				('tbsc', 30),
				('tron', 20),
				('ttron', 20)
			) AS v(id, confirmations)
			WHERE c.id = v.id AND c.finality <> 'confirmations';
		END IF;

		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'transactions' AND column_name = 'finality'
		) THEN
			UPDATE transactions AS t SET
				required_confs = c.required_confirmations,
				updated_at = NOW()
			FROM chains AS c
			WHERE t.chain = c.id
			  AND t.finality <> 'confirmations'
			  AND t.status IN ('pending', 'confirming')
			  AND t.required_confs < c.required_confirmations;
		END IF;

		ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_finality_valid;
		ALTER TABLE transactions DROP COLUMN IF EXISTS finality;
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_finality_fits_adapter;
		ALTER TABLE chains DROP COLUMN IF EXISTS finality;
	END $$`)
}
