package migrations

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
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
	rows, err := platformSettingsSeed(os.Getenv)
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

func insertPlatformSetting(row platformSeedRow) error {
	if row.Group == "" || row.Key == "" || row.Value == "" {
		return fmt.Errorf("insert platform setting: group, key, and value are required")
	}
	if _, err := migrationQuery().Exec(platformSettingsInsert, row.Group, row.Key, row.Value); err != nil {
		return fmt.Errorf("insert platform setting %s %s: %w", row.Group, row.Key, err)
	}
	return nil
}

// platformSeedField is one platform settings field that falls back to an
// environment variable. integer fields are stored as the trimmed text of an
// integer; options, when set, close the vocabulary of a string field.
type platformSeedField struct {
	group   string
	key     string
	env     string
	secret  bool
	integer bool
	options []string
}

// platformSeedFields is the frozen copy of what settings.PlatformSettingsSeed
// visited when this migration was written: the mail, price and webhook/block
// height provider groups, in registry order. Feature flags are not settings
// rows and are not seeded.
var platformSeedFields = []platformSeedField{
	{group: "mail_smtp", key: "host", env: "MAIL_HOST"},
	{group: "mail_smtp", key: "port", env: "MAIL_PORT", integer: true},
	{group: "mail_smtp", key: "encryption", env: "MAIL_ENCRYPTION", options: []string{"none", "tls", "starttls"}},
	{group: "mail_smtp", key: "username", env: "MAIL_USERNAME"},
	{group: "mail_smtp", key: "password", env: "MAIL_PASSWORD", secret: true},
	{group: "mail_delivery", key: "from_address", env: "MAIL_FROM_ADDRESS"},
	{group: "mail_delivery", key: "from_name", env: "MAIL_FROM_NAME"},
	{group: "price_coingecko", key: "api_key", env: "COINGECKO_API_KEY", secret: true},
	{group: "price_coinmarketcap", key: "api_key", env: "COINMARKETCAP_API_KEY", secret: true},
	{group: "price_coinapi", key: "api_key", env: "COINAPI_KEY", secret: true},
	{group: "provider_alchemy", key: "auth_token", env: "ALCHEMY_AUTH_TOKEN", secret: true},
	{group: "provider_helius", key: "api_key", env: "HELIUS_API_KEY", secret: true},
	{group: "provider_quicknode", key: "api_key", env: "QUICKNODE_API_KEY", secret: true},
	{group: "provider_etherscan", key: "api_key", env: "ETHERSCAN_API_KEY", secret: true},
}

// platformSeedRow is one platform settings insert. A secret Value is sealed and
// must not be logged.
type platformSeedRow struct {
	Group string
	Key   string
	Value string
}

// platformSettingsSeed copies the non-blank env fallbacks. lookup reads one
// environment variable. Nothing in the returned rows is written to the log.
func platformSettingsSeed(lookup func(string) string) ([]platformSeedRow, error) {
	var rows []platformSeedRow
	for _, field := range platformSeedFields {
		raw := lookup(field.env)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		stored, err := platformSeedValue(field, raw)
		if err != nil {
			return nil, fmt.Errorf("platform settings seed %s %s: %w", field.group, field.key, err)
		}
		rows = append(rows, platformSeedRow{Group: field.group, Key: field.key, Value: stored})
	}
	return rows, nil
}

func platformSeedValue(field platformSeedField, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if field.secret {
		cipher := migrationCipher()
		if cipher == nil {
			return "", fmt.Errorf("seal setting: crypt is not available")
		}
		sealed, err := sealSetting(cipher, trimmed)
		if err != nil || sealed == "" || !isSealedSetting(sealed) {
			return "", fmt.Errorf("seal failed")
		}
		return sealed, nil
	}
	if field.integer {
		if _, err := strconv.Atoi(trimmed); err != nil {
			return "", fmt.Errorf("value is not storable")
		}
		return trimmed, nil
	}
	if len(field.options) > 0 && !slices.Contains(field.options, trimmed) {
		return "", fmt.Errorf("value is not storable")
	}
	return trimmed, nil
}
