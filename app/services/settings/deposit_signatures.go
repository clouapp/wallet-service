package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// DepositSignatureValues is the stored deposit_signatures row. ChainsStored tells a
// stored chain list (Chains, possibly empty: the sync turned off) from none (the
// environment's list stands). A zero number is missing or invalid: the sync keeps the
// environment value for that field.
type DepositSignatureValues struct {
	Chains          []string
	ChainsStored    bool
	IntervalSeconds int
	Concurrency     int
	MaxPages        int
}

// EffectiveDepositSignatures reads the platform deposit_signatures group at the
// moment of use, like EffectiveDepositScan: a cache hit skips the database, a missing
// row leaves every field unset, one invalid key falls back for that key only, and a
// database failure is returned so the caller keeps the environment values and the
// deposits are still read.
func (s *Service) EffectiveDepositSignatures(ctx context.Context) (DepositSignatureValues, error) {
	if s == nil {
		return DepositSignatureValues{}, errServiceRequired
	}
	if ctx == nil {
		return DepositSignatureValues{}, fmt.Errorf("deposit signature settings: context is required")
	}
	group, ok := FindGroup(groupDepositSignatures)
	if !ok {
		return DepositSignatureValues{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group.Name)
	if err != nil {
		return DepositSignatureValues{}, err
	}
	return parseDepositSignatures(stored), nil
}

func parseDepositSignatures(stored map[string]string) DepositSignatureValues {
	values := DepositSignatureValues{
		IntervalSeconds: signatureOverride(stored, keySignatureInterval, minSignatureIntervalSeconds, 0),
		Concurrency:     signatureOverride(stored, keySignatureConcurrency, 1, maxSignatureConcurrency),
		MaxPages:        signatureOverride(stored, keySignatureMaxPages, 1, maxSignatureMaxPages),
	}
	if raw, ok := stored[keySignatureChains]; ok {
		values.ChainsStored = true
		values.Chains = splitList(raw)
	}
	return values
}

// signatureOverride returns a stored integer from floor up to ceiling (0: no upper
// bound). Missing, blank and "0" values return 0 quietly; unparsable or out-of-range
// ones return 0 with a warning that names the key only.
func signatureOverride(stored map[string]string, key string, floor, ceiling int) int {
	raw, ok := stored[key]
	if !ok || strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "0" {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < floor || (ceiling > 0 && parsed > ceiling) {
		slog.Warn("deposit signature setting fell back to the environment default", "key", key)
		return 0
	}
	return parsed
}
