package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/facades"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

type thresholdDef struct {
	chainID         string
	gasReadinessRaw string
	dustNativeRaw   string
	dustUSD         numeric.NullDecimal
}

// SeedSweepThresholds populates per-chain sweep + gas readiness thresholds on the `chains` table.
// An empty gas_readiness_threshold_raw means "not applicable" (BTC) and is stored as an empty
// string: the column is NOT NULL, and the model reads "" the same way it used to read NULL.
// Bitcoin dust USD is 0, the value config.SweepDefaults already uses for "no tokens on BTC".
func SeedSweepThresholds(_ context.Context) error {
	defs, err := sweepThresholdDefs()
	if err != nil {
		return err
	}

	const sql = `UPDATE chains SET
		gas_readiness_threshold_raw = ?,
		dust_threshold_native_raw   = ?,
		dust_threshold_usd          = ?
		WHERE id = ?`

	for _, d := range defs {
		if err := validateThresholdDef(d); err != nil {
			return err
		}
		if _, err := facades.Orm().Query().Exec(sql, d.gasReadinessRaw, d.dustNativeRaw, d.dustUSD, d.chainID); err != nil {
			slog.Error("seed sweep thresholds failed", "chain", d.chainID, "error", err)
			return err
		}
	}
	slog.Info("sweep thresholds seeded", "chains", len(defs))
	return nil
}

func sweepThresholdDefs() ([]thresholdDef, error) {
	dustHigh := numeric.NewNullDecimal(decimal.New(1, 0))
	dustLow := numeric.NewNullDecimal(decimal.New(1, -1))
	dustNone := numeric.NewNullDecimal(decimal.Zero)

	defs := []thresholdDef{
		{models.ChainETH, "5000000000000000", "500000000000000", dustHigh},
		{models.ChainTETH, "5000000000000000", "500000000000000", dustHigh},
		{models.ChainPolygon, "500000000000000000", "100000000000000000", dustLow},
		{models.ChainTPolygon, "500000000000000000", "100000000000000000", dustLow},
		{models.ChainSOL, "10000000", "1000000", dustHigh},
		{models.ChainTSOL, "10000000", "1000000", dustHigh},
		{models.ChainBTC, "", "10000", dustNone},
		{models.ChainTBTC, "", "10000", dustNone},
	}
	for _, chainID := range AddedEVMChainIDs {
		thresholds, err := addedChainThresholds(chainID)
		if err != nil {
			return nil, err
		}
		defs = append(defs, thresholdDef{
			chainID,
			derefOrEmpty(thresholds.gasReadinessRaw),
			derefOrEmpty(thresholds.dustNativeRaw),
			thresholds.dustUSD,
		})
	}
	return defs, nil
}

func seedThresholdsFor(chainID string) (*seedThresholds, error) {
	defs, err := sweepThresholdDefs()
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if d.chainID != chainID {
			continue
		}
		if err := validateThresholdDef(d); err != nil {
			return nil, err
		}
		gas := d.gasReadinessRaw
		dust := d.dustNativeRaw
		return &seedThresholds{
			gasReadinessRaw: &gas,
			dustNativeRaw:   &dust,
			dustUSD:         d.dustUSD,
		}, nil
	}
	return nil, fmt.Errorf("no sweep thresholds for chain %s", chainID)
}

func validateThresholdDef(d thresholdDef) error {
	if err := requireUnsignedRaw(d.chainID, "gas_readiness_threshold_raw", d.gasReadinessRaw, true); err != nil {
		return err
	}
	if err := requireUnsignedRaw(d.chainID, "dust_threshold_native_raw", d.dustNativeRaw, false); err != nil {
		return err
	}
	if !d.dustUSD.Valid {
		return fmt.Errorf("seed sweep thresholds for chain %s: dust_threshold_usd is required", d.chainID)
	}
	if d.dustUSD.Decimal.IsNegative() {
		return fmt.Errorf("seed sweep thresholds for chain %s: dust_threshold_usd must not be negative", d.chainID)
	}
	if err := models.DustThresholdUSDColumn.Validate(d.dustUSD.Decimal); err != nil {
		return fmt.Errorf("seed sweep thresholds for chain %s: %w", d.chainID, err)
	}
	return nil
}

func requireUnsignedRaw(chainID, column, value string, emptyAllowed bool) error {
	if value == "" {
		if emptyAllowed {
			return nil
		}
		return fmt.Errorf("seed sweep thresholds for chain %s: %s is required", chainID, column)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return fmt.Errorf("seed sweep thresholds for chain %s: %s %q must be an unsigned integer", chainID, column, value)
		}
	}
	return nil
}
