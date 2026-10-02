package migrations

import "github.com/goravel/framework/facades"

// M00000000000320AddTotpLastUsedCounterToUsers records the last TOTP time-step a
// user redeemed, so a code that was already accepted cannot be accepted again
// while it is still inside the validation window.
type M00000000000320AddTotpLastUsedCounterToUsers struct{}

func (r *M00000000000320AddTotpLastUsedCounterToUsers) Signature() string {
	return "00000000000320_add_totp_last_used_counter_to_users"
}

func (r *M00000000000320AddTotpLastUsedCounterToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_used_counter BIGINT NOT NULL DEFAULT 0`,
	)
	return err
}

func (r *M00000000000320AddTotpLastUsedCounterToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS totp_last_used_counter`)
	return err
}
