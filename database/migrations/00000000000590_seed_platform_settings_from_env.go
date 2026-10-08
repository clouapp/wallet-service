package migrations

import (
	"fmt"
	"os"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/services/settings"
)

// M00000000000590SeedPlatformSettingsFromEnv is S1.4.3:
// PlatformSettingsSeed per group (mail_*, price_*, providers_*), values sealed
// in bootstrap, blanks skipped, ON CONFLICT DO NOTHING.
//
// Appendix B only says "300 seed platform settings from env". 300 is already
// 00000000000300_create_features_table, so this step is 590.
//
// The insert targets settings_platform_entry_idx, the partial unique index on
// ("group", "key") WHERE account_id IS NULL. A second migrate does not
// overwrite a row an operator saved.
type M00000000000590SeedPlatformSettingsFromEnv struct{}

func (r *M00000000000590SeedPlatformSettingsFromEnv) Signature() string {
	return "00000000000590_seed_platform_settings_from_env"
}

func (r *M00000000000590SeedPlatformSettingsFromEnv) Up() error {
	rows, err := settings.PlatformSettingsSeed(os.Getenv, settings.CryptSealer{})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := insertPlatformSetting(row); err != nil {
			return err
		}
	}
	return nil
}

// Down does nothing. The insert does not record which rows it added, and an
// operator may have changed a seeded value. Deleting by group and key would
// remove that edit. A no-op is the rollback that cannot discard it.
func (r *M00000000000590SeedPlatformSettingsFromEnv) Down() error {
	return nil
}

// platformSettingsInsert conflicts on settings_platform_entry_idx.
const platformSettingsInsert = `INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
VALUES (NULL, ?, ?, ?, NOW(), NOW())
ON CONFLICT ("group", "key") WHERE account_id IS NULL DO NOTHING`

func insertPlatformSetting(row settings.PlatformSeedRow) error {
	if row.Group == "" || row.Key == "" || row.Value == "" {
		return fmt.Errorf("insert platform setting: group, key, and value are required")
	}
	if _, err := facades.Orm().Query().Exec(platformSettingsInsert, row.Group, row.Key, row.Value); err != nil {
		return fmt.Errorf("insert platform setting %s %s: %w", row.Group, row.Key, err)
	}
	return nil
}
