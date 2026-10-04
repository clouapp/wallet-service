package migrations

// M00000000000340AccessTokensSpendingLimitNonNegative refuses a negative
// daily_usd inside access_tokens.spending_limit. A blank cap ({}, null, or a
// missing daily_usd) stays allowed. This migration does not change other columns.
type M00000000000340AccessTokensSpendingLimitNonNegative struct{}

func (r *M00000000000340AccessTokensSpendingLimitNonNegative) Signature() string {
	return "00000000000340_access_tokens_spending_limit_non_negative"
}

func (r *M00000000000340AccessTokensSpendingLimitNonNegative) Up() error {
	if err := execMigrationSQL(`ALTER TABLE access_tokens DROP CONSTRAINT IF EXISTS access_tokens_spending_limit_daily_usd_non_negative`); err != nil {
		return err
	}
	return execMigrationSQL(`ALTER TABLE access_tokens
		ADD CONSTRAINT access_tokens_spending_limit_daily_usd_non_negative
		CHECK (
			spending_limit IS NULL
			OR jsonb_typeof(spending_limit::jsonb) IS DISTINCT FROM 'object'
			OR NOT jsonb_exists(spending_limit::jsonb, 'daily_usd')
			OR jsonb_typeof(spending_limit::jsonb -> 'daily_usd') = 'null'
			OR btrim(spending_limit::jsonb ->> 'daily_usd') = ''
			OR btrim(spending_limit::jsonb ->> 'daily_usd') ~ '^(0|[1-9][0-9]*)([.][0-9]+){0,1}$'
		)`)
}

func (r *M00000000000340AccessTokensSpendingLimitNonNegative) Down() error {
	return execMigrationSQL(`ALTER TABLE access_tokens DROP CONSTRAINT IF EXISTS access_tokens_spending_limit_daily_usd_non_negative`)
}
