package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// DepositScanValues is the stored deposit_scan row. A zero field is missing
// or invalid: the scanner keeps the environment default for that field.
type DepositScanValues struct {
	BatchBlocks   int
	CatchUpBlocks int
	Concurrency   int
}

// platformReader loads one platform group. Account rows are not part of it.
type platformReader interface {
	ListPlatform(ctx context.Context, group string) ([]models.Setting, error)
}

// EffectiveDepositScan reads the platform deposit_scan group at the moment
// of use. A cache hit skips the database. A cache miss, a cache failure, or
// a value that is not a JSON object reads the database. A missing row leaves every
// field zero so the caller keeps the environment window. One invalid key
// falls back to zero for that key only. A database failure is returned so
// the caller can keep the environment window and still run the scan.
func (s *Service) EffectiveDepositScan(ctx context.Context) (DepositScanValues, error) {
	if s == nil {
		return DepositScanValues{}, errServiceRequired
	}
	if ctx == nil {
		return DepositScanValues{}, fmt.Errorf("deposit scan settings: context is required")
	}
	group, ok := FindGroup(groupDepositScan)
	if !ok {
		return DepositScanValues{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group.Name)
	if err != nil {
		return DepositScanValues{}, err
	}
	return parseDepositScan(stored), nil
}

func parseDepositScan(stored map[string]string) DepositScanValues {
	return DepositScanValues{
		BatchBlocks:   scanOverride(stored, keyBatchBlocks, 0),
		CatchUpBlocks: scanOverride(stored, keyCatchUpBlocks, 0),
		Concurrency:   scanOverride(stored, keyScanConcurrency, maxDepositScanConcurrency),
	}
}

// scanOverride returns a positive stored integer. Missing, blank, unparsable,
// non-positive, and above-ceiling values return 0 so the environment default
// stands. ceiling 0 means no upper bound.
func scanOverride(stored map[string]string, key string, ceiling int) int {
	raw, ok := stored[key]
	if !ok {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 || (ceiling > 0 && parsed > ceiling) {
		slog.Warn("deposit scan setting fell back to the environment default", "key", key)
		return 0
	}
	return parsed
}
