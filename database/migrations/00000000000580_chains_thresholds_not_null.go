package migrations

// M00000000000580ChainsThresholdsNotNull backfills the sweep thresholds on
// chains and then requires them. Appendix B item 460 names this step. The plan
// number 460 is 00000000000460_add_sessions_revoked_at_to_users, so this step
// is 580.
//
// The numbers are the seeder literals in database/seeds/sweep_thresholds.go,
// which match the defaults in config.SweepDefaults. Bitcoin has no gas
// threshold in either place (the seed stored that as NULL via NULLIF of an
// empty string); the column keeps the empty string, which the model already
// reads as "not set". Bitcoin dust USD is NULL in the seeder and decimal.Zero
// in SweepDefaults ("N/A: no tokens on BTC"); the backfill stores 0, the only
// non-null value the code already uses. A chain id that is still NULL and is
// not in that list fails the migration instead of receiving an invented number.
//
// Down drops NOT NULL, the defaults, and the non-negative checks. It leaves
// every backfilled value in place.
type M00000000000580ChainsThresholdsNotNull struct{}

func (r *M00000000000580ChainsThresholdsNotNull) Signature() string {
	return "00000000000580_chains_thresholds_not_null"
}

func (r *M00000000000580ChainsThresholdsNotNull) Up() error {
	return execMigrationSQL(`DO $$
	DECLARE
		missing text;
		invalid text;
	BEGIN
		UPDATE chains AS c SET
			gas_readiness_threshold_raw = COALESCE(c.gas_readiness_threshold_raw, v.gas),
			dust_threshold_native_raw = COALESCE(c.dust_threshold_native_raw, v.dust_native),
			dust_threshold_usd = COALESCE(c.dust_threshold_usd, v.dust_usd)
		FROM (VALUES
			('eth'::varchar, '5000000000000000'::text, '500000000000000'::text, 1.0000::numeric),
			('teth', '5000000000000000', '500000000000000', 1.0000),
			('polygon', '500000000000000000', '100000000000000000', 0.1000),
			('tpolygon', '500000000000000000', '100000000000000000', 0.1000),
			('sol', '10000000', '1000000', 1.0000),
			('tsol', '10000000', '1000000', 1.0000),
			('base', '200000000000000', '20000000000000', 0.1000),
			('tbase', '200000000000000', '20000000000000', 0.1000),
			('arbitrum', '200000000000000', '20000000000000', 0.1000),
			('tarbitrum', '200000000000000', '20000000000000', 0.1000),
			('bsc', '500000000000000', '50000000000000', 0.1000),
			('tbsc', '500000000000000', '50000000000000', 0.1000),
			('btc', '', '10000', 0),
			('tbtc', '', '10000', 0)
		) AS v(id, gas, dust_native, dust_usd)
		WHERE c.id = v.id
		  AND (
			c.gas_readiness_threshold_raw IS NULL
			OR c.dust_threshold_native_raw IS NULL
			OR c.dust_threshold_usd IS NULL
		  );

		SELECT string_agg(id, ', ' ORDER BY id) INTO missing
		FROM chains
		WHERE gas_readiness_threshold_raw IS NULL
		   OR dust_threshold_native_raw IS NULL
		   OR dust_threshold_usd IS NULL;
		IF missing IS NOT NULL THEN
			RAISE EXCEPTION 'chains threshold backfill has no seed value for %', missing;
		END IF;

		SELECT string_agg(id, ', ' ORDER BY id) INTO invalid
		FROM chains
		WHERE gas_readiness_threshold_raw !~ '^[0-9]*$'
		   OR dust_threshold_native_raw !~ '^[0-9]*$'
		   OR dust_threshold_usd < 0;
		IF invalid IS NOT NULL THEN
			RAISE EXCEPTION 'chains thresholds must be non-negative for %', invalid;
		END IF;

		ALTER TABLE chains ALTER COLUMN gas_readiness_threshold_raw SET DEFAULT '';
		ALTER TABLE chains ALTER COLUMN dust_threshold_native_raw SET DEFAULT '';
		ALTER TABLE chains ALTER COLUMN dust_threshold_usd SET DEFAULT 0;
		ALTER TABLE chains ALTER COLUMN gas_readiness_threshold_raw SET NOT NULL;
		ALTER TABLE chains ALTER COLUMN dust_threshold_native_raw SET NOT NULL;
		ALTER TABLE chains ALTER COLUMN dust_threshold_usd SET NOT NULL;

		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_gas_readiness_threshold_raw_non_negative;
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_dust_threshold_native_raw_non_negative;
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_dust_threshold_usd_non_negative;
		ALTER TABLE chains
			ADD CONSTRAINT chains_gas_readiness_threshold_raw_non_negative
			CHECK (gas_readiness_threshold_raw ~ '^[0-9]*$');
		ALTER TABLE chains
			ADD CONSTRAINT chains_dust_threshold_native_raw_non_negative
			CHECK (dust_threshold_native_raw ~ '^[0-9]*$');
		ALTER TABLE chains
			ADD CONSTRAINT chains_dust_threshold_usd_non_negative
			CHECK (dust_threshold_usd >= 0);
	END $$`)
}

func (r *M00000000000580ChainsThresholdsNotNull) Down() error {
	return execMigrationSQL(`DO $$
	BEGIN
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_gas_readiness_threshold_raw_non_negative;
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_dust_threshold_native_raw_non_negative;
		ALTER TABLE chains DROP CONSTRAINT IF EXISTS chains_dust_threshold_usd_non_negative;
		ALTER TABLE chains ALTER COLUMN gas_readiness_threshold_raw DROP NOT NULL;
		ALTER TABLE chains ALTER COLUMN dust_threshold_native_raw DROP NOT NULL;
		ALTER TABLE chains ALTER COLUMN dust_threshold_usd DROP NOT NULL;
		ALTER TABLE chains ALTER COLUMN gas_readiness_threshold_raw DROP DEFAULT;
		ALTER TABLE chains ALTER COLUMN dust_threshold_native_raw DROP DEFAULT;
		ALTER TABLE chains ALTER COLUMN dust_threshold_usd DROP DEFAULT;
	END $$`)
}
