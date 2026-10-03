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

// SeedSweepThresholds populates per-chain sweep + gas readiness thresholds on the `chains` table.
// Empty strings for gas_readiness_threshold_raw mean "not applicable" (e.g. BTC) and are coerced
// to SQL NULL via NULLIF. A nil dustUSD yields SQL NULL (no USD-equivalent token dust on BTC).
func SeedSweepThresholds(_ context.Context) error {
	type thresholdDef struct {
		chainID         string
		gasReadinessRaw string
		dustNativeRaw   string
		dustUSD         numeric.NullDecimal
	}
	dustHigh := numeric.NewNullDecimal(decimal.New(1, 0))
	dustLow := numeric.NewNullDecimal(decimal.New(1, -1))
	noDust := numeric.NullDecimal{}

	defs := []thresholdDef{
		{models.ChainETH, "5000000000000000", "500000000000000", dustHigh},
		{models.ChainTETH, "5000000000000000", "500000000000000", dustHigh},
		{models.ChainPolygon, "500000000000000000", "100000000000000000", dustLow},
		{models.ChainTPolygon, "500000000000000000", "100000000000000000", dustLow},
		{models.ChainSOL, "10000000", "1000000", dustHigh},
		{models.ChainTSOL, "10000000", "1000000", dustHigh},
		{models.ChainBTC, "", "10000", noDust},
		{models.ChainTBTC, "", "10000", noDust},
	}
	for _, chainID := range AddedEVMChainIDs {
		thresholds, err := addedChainThresholds(chainID)
		if err != nil {
			return err
		}
		defs = append(defs, thresholdDef{chainID, derefOrEmpty(thresholds.gasReadinessRaw), derefOrEmpty(thresholds.dustNativeRaw), thresholds.dustUSD})
	}

	const sql = `UPDATE chains SET
		gas_readiness_threshold_raw = NULLIF(?, ''),
		dust_threshold_native_raw   = NULLIF(?, ''),
		dust_threshold_usd          = ?
		WHERE id = ?`

	for _, d := range defs {
		if d.dustUSD.Valid {
			if err := models.DustThresholdUSDColumn.Validate(d.dustUSD.Decimal); err != nil {
				return fmt.Errorf("seed sweep thresholds for chain %s: %w", d.chainID, err)
			}
		}
		if _, err := facades.Orm().Query().Exec(sql, d.gasReadinessRaw, d.dustNativeRaw, d.dustUSD, d.chainID); err != nil {
			slog.Error("seed sweep thresholds failed", "chain", d.chainID, "error", err)
			return err
		}
	}
	slog.Info("sweep thresholds seeded", "chains", len(defs))
	return nil
}
