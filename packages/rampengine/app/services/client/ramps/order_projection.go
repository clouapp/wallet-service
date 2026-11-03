package ramps

import (
	"encoding/json"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func decodeEstimateSnapshot(raw string) (*estimateSnapshot, bool) {
	if raw == "" {
		return nil, false
	}
	var snap estimateSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

func collectEstimateIDs(rows []models.Transaction) []uint {
	seen := make(map[uint]struct{})
	ids := make([]uint, 0)
	for i := range rows {
		if rows[i].EstimateID == nil {
			continue
		}
		id := *rows[i].EstimateID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
