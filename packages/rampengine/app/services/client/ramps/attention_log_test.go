package ramps_test

import (
	"testing"

	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// A recovery that is out of attempts hands the row to an operator
// (needs_operator) with a line starting ramp.attention:. Only the prefix is
// pinned, plus that no amount or address reaches it.
func TestAttentionMarker_RecoveryExhausted(t *testing.T) {
	logger := &mockslog.Log{}
	lines := support.CaptureErrorLines(logger)
	h := buildRecoveryHarness(t, logger)
	h.auth.RecoveryAttempts = 8

	assert.Equal(t, 1, h.recover(t).Escalated)
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
	lines.RequireAttention(t, wallet, "100.00")
}
