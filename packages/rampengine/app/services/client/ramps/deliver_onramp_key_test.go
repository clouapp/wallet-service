package ramps

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestDeliverOnrampIdempotencyKey(t *testing.T) {
	t.Parallel()
	ext := "550e8400-e29b-41d4-a716-446655440000"
	tx0 := &models.Transaction{ExternalID: ext, DeliveryAttempts: 0}
	tx1 := &models.Transaction{ExternalID: ext, DeliveryAttempts: 1}
	// The first attempt keeps the released key, so a send placed before the
	// deploy is replayed upstream rather than placed again.
	require.Equal(t, "onramp-deliver-550e8400e29b41d4a716446655440000", deliveryIdempotencyKey(tx0))
	require.Equal(t, "onramp-delivery:"+ext+":1", deliveryIdempotencyKey(tx1))
}
