package migrations

import "fmt"

// M00000000000530DropAccountsSweepLimits drops accounts.sweep_limits
// (Appendix B, 320) after 520 copied recognized keys into account_sweep_limits.
// Sweep and the dashboard field read that settings group. Down adds the
// column back as nullable JSON and leaves every row empty: the copied
// settings are not written back into the old document.
type M00000000000530DropAccountsSweepLimits struct{}

func (r *M00000000000530DropAccountsSweepLimits) Signature() string {
	return "00000000000530_drop_accounts_sweep_limits"
}

func (r *M00000000000530DropAccountsSweepLimits) Up() error {
	if _, err := migrationQuery().Exec(`ALTER TABLE accounts DROP COLUMN sweep_limits`); err != nil {
		return fmt.Errorf("drop accounts.sweep_limits: %w", err)
	}
	return nil
}

func (r *M00000000000530DropAccountsSweepLimits) Down() error {
	statements := []string{
		`ALTER TABLE accounts ADD COLUMN sweep_limits JSONB`,
		`COMMENT ON COLUMN accounts.sweep_limits IS 'Per-account overrides for sweep rate limits and velocity caps'`,
	}
	for _, statement := range statements {
		if _, err := migrationQuery().Exec(statement); err != nil {
			return fmt.Errorf("restore accounts.sweep_limits: %w", err)
		}
	}
	return nil
}
