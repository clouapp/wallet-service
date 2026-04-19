package seeds

import (
	"context"
	"log/slog"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// SeedSweepThresholds populates per-chain sweep + gas readiness thresholds on the `chains` table.
// Empty strings for gas_readiness_threshold_raw mean "not applicable" (e.g. BTC) and are coerced
// to SQL NULL via NULLIF. A nil dustUSD yields SQL NULL (no USD-equivalent token dust on BTC).
func SeedSweepThresholds(_ context.Context) error {
	type thresholdDef struct {
		chainID         string
		gasReadinessRaw string
		dustNativeRaw   string
		dustUSD         *float64
	}
	p := func(f float64) *float64 { return &f }

	defs := []thresholdDef{
		{models.ChainETH, "5000000000000000", "500000000000000", p(1.0)},
		{models.ChainTETH, "5000000000000000", "500000000000000", p(1.0)},
		{models.ChainPolygon, "500000000000000000", "100000000000000000", p(0.1)},
		{models.ChainTPolygon, "500000000000000000", "100000000000000000", p(0.1)},
		{models.ChainSOL, "10000000", "1000000", p(1.0)},
		{models.ChainTSOL, "10000000", "1000000", p(1.0)},
		{models.ChainBTC, "", "10000", nil},
		{models.ChainTBTC, "", "10000", nil},
	}

	const sql = `UPDATE chains SET
		gas_readiness_threshold_raw = NULLIF(?, ''),
		dust_threshold_native_raw   = NULLIF(?, ''),
		dust_threshold_usd          = ?
		WHERE id = ?`

	for _, d := range defs {
		if _, err := facades.Orm().Query().Exec(sql, d.gasReadinessRaw, d.dustNativeRaw, d.dustUSD, d.chainID); err != nil {
			slog.Error("seed sweep thresholds failed", "chain", d.chainID, "error", err)
			return err
		}
	}
	slog.Info("sweep thresholds seeded", "chains", len(defs))
	return nil
}
