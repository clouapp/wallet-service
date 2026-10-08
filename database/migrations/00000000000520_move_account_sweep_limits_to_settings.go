package migrations

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
)

const accountSweepLimitsGroup = "account_sweep_limits"

// M00000000000520MoveAccountSweepLimitsToSettings copies accounts.sweep_limits
// into account_sweep_limits rows (Appendix B, 310). This migration leaves the
// column in place; 530 drops it. A row already stored for the same key is left
// alone. A blank cap is unlimited and is not inserted. A negative amount is
// refused and nothing from that run is kept.
type M00000000000520MoveAccountSweepLimitsToSettings struct{}

func (r *M00000000000520MoveAccountSweepLimitsToSettings) Signature() string {
	return "00000000000520_move_account_sweep_limits_to_settings"
}

func (r *M00000000000520MoveAccountSweepLimitsToSettings) Up() error {
	return rewriteAccountSweepLimits(false)
}

func (r *M00000000000520MoveAccountSweepLimitsToSettings) Down() error {
	return rewriteAccountSweepLimits(true)
}

type accountSweepLimitsRow struct {
	ID          uuid.UUID
	SweepLimits string
}

func rewriteAccountSweepLimits(remove bool) error {
	return migrationTransaction(func(tx orm.Query) error {
		var rows []accountSweepLimitsRow
		err := tx.Raw(`SELECT id, sweep_limits::text AS sweep_limits
			FROM accounts
			WHERE sweep_limits IS NOT NULL
			FOR UPDATE`).Scan(&rows)
		if err != nil {
			return fmt.Errorf("load account sweep limits: %w", err)
		}
		for _, row := range rows {
			copied, err := accountSweepLimitSettings(row.SweepLimits)
			if err != nil {
				return fmt.Errorf("account %s sweep_limits: %w", row.ID, err)
			}
			for _, key := range sweepLimitSettingOrder {
				value, ok := copied[key]
				if !ok {
					continue
				}
				if remove {
					if err := deleteCopiedSweepLimit(tx, row.ID, key, value); err != nil {
						return err
					}
					continue
				}
				if err := insertCopiedSweepLimit(tx, row.ID, key, value); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func insertCopiedSweepLimit(tx orm.Query, accountID uuid.UUID, key, value string) error {
	_, err := tx.Exec(`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		VALUES (?, ?, ?, ?, NOW(), NOW())
		ON CONFLICT (account_id, "group", "key") WHERE account_id IS NOT NULL DO NOTHING`,
		accountID, accountSweepLimitsGroup, key, value)
	if err != nil {
		return fmt.Errorf("account %s sweep limit %s: %w", accountID, key, err)
	}
	return nil
}

func deleteCopiedSweepLimit(tx orm.Query, accountID uuid.UUID, key, value string) error {
	_, err := tx.Exec(`DELETE FROM settings
		WHERE account_id = ? AND "group" = ? AND "key" = ? AND value = ?`,
		accountID, accountSweepLimitsGroup, key, value)
	if err != nil {
		return fmt.Errorf("account %s sweep limit %s: %w", accountID, key, err)
	}
	return nil
}

var sweepLimitSettingOrder = []string{
	"max_addresses_evm",
	"max_addresses_solana",
	"max_addresses_bitcoin",
	"max_consolidate_requests_per_day",
	"daily_withdraw_cap_usd",
}

var sweepLimitChainKeys = map[string]string{
	"evm":     "max_addresses_evm",
	"sol":     "max_addresses_solana",
	"solana":  "max_addresses_solana",
	"btc":     "max_addresses_bitcoin",
	"bitcoin": "max_addresses_bitcoin",
}

func accountSweepLimitSettings(document string) (map[string]string, error) {
	trimmed := strings.TrimSpace(document)
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return map[string]string{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON")
	}
	if fields == nil {
		return nil, fmt.Errorf("invalid JSON")
	}
	copied := map[string]string{}
	if raw, ok := fields["max_addresses_per_request"]; ok {
		if err := assignNestedAddressCaps(copied, raw); err != nil {
			return nil, err
		}
	}
	for _, key := range sweepLimitSettingOrder[:4] {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		value, present, err := positiveIntSetting(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if !present {
			continue
		}
		if err := assignSweepLimit(copied, key, value); err != nil {
			return nil, err
		}
	}
	if raw, ok := fields["daily_withdraw_cap_usd"]; ok {
		value, present, err := nonNegativeDecimalSetting(raw)
		if err != nil {
			return nil, fmt.Errorf("daily_withdraw_cap_usd: %w", err)
		}
		if present {
			if err := assignSweepLimit(copied, "daily_withdraw_cap_usd", value); err != nil {
				return nil, err
			}
		}
	}
	return copied, nil
}

func assignNestedAddressCaps(copied map[string]string, raw json.RawMessage) error {
	if isJSONNull(raw) {
		return nil
	}
	var chains map[string]json.RawMessage
	if err := json.Unmarshal(raw, &chains); err != nil || chains == nil {
		return fmt.Errorf("max_addresses_per_request: expected an object")
	}
	for chain, value := range chains {
		key, ok := sweepLimitChainKeys[strings.ToLower(strings.TrimSpace(chain))]
		if !ok {
			return fmt.Errorf("max_addresses_per_request: unknown chain")
		}
		parsed, present, err := positiveIntSetting(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if !present {
			continue
		}
		if err := assignSweepLimit(copied, key, parsed); err != nil {
			return err
		}
	}
	return nil
}

func assignSweepLimit(copied map[string]string, key, value string) error {
	existing, ok := copied[key]
	if ok && existing != value {
		return fmt.Errorf("%s is set twice with different values", key)
	}
	copied[key] = value
	return nil
}

func positiveIntSetting(raw json.RawMessage) (string, bool, error) {
	text, present, err := scalarText(raw)
	if err != nil || !present {
		return "", present, err
	}
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return "", false, fmt.Errorf("expected an integer greater than 0")
	}
	if parsed <= 0 {
		return "", false, fmt.Errorf("must be greater than 0")
	}
	return strconv.Itoa(parsed), true, nil
}

func nonNegativeDecimalSetting(raw json.RawMessage) (string, bool, error) {
	text, present, err := scalarText(raw)
	if err != nil || !present {
		return "", present, err
	}
	if text == "" {
		return "", false, nil
	}
	parsed, ok := new(big.Rat).SetString(text)
	if !ok || parsed == nil {
		return "", false, fmt.Errorf("expected a decimal number")
	}
	if parsed.Sign() < 0 {
		return "", false, fmt.Errorf("must be greater than or equal to 0")
	}
	return text, true, nil
}

func scalarText(raw json.RawMessage) (string, bool, error) {
	if isJSONNull(raw) {
		return "", false, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", false, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", false, fmt.Errorf("expected a scalar")
		}
		return strings.TrimSpace(text), true, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return "", false, fmt.Errorf("expected a scalar")
	}
	return trimmed, true, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null" || len(raw) == 0
}
