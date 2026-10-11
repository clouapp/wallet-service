package sweep

import (
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

// Every row the executor broadcasts (gas_seed, sweep and the withdrawal) is
// confirmed by the finality policy of its chain at broadcast, like required_confs.
func TestExecute_Rows_RecordTheChainFinalityPolicy(t *testing.T) {
	mockChain := sweepMockChain("eth", "eth")
	mockChain.FinalityPolicyVal = models.FinalityFinalizedTag
	mockChain.RequiredConfirmationsVal = 3

	rows := executeSingleLegSweep(t, mockChain, "usdt", big.NewInt(600))

	if len(rows) != 3 {
		t.Fatalf("persisted %d rows, want gas_seed, sweep and withdrawal", len(rows))
	}
	for _, row := range rows {
		if row.Finality != models.FinalityFinalizedTag || row.RequiredConfs != 3 {
			t.Fatalf("%s row recorded finality %q with %d required confirmations", row.TxType, row.Finality, row.RequiredConfs)
		}
	}
}
