package txkind

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		txType, origin, chainDirection string
		want                           Kind
	}{
		"deposit":                          {models.TxTypeDeposit, "", models.TxDirectionInbound, Kind{TypeDeposit, DirectionIncoming}},
		"deposit without direction":        {models.TxTypeDeposit, "", "", Kind{TypeDeposit, DirectionIncoming}},
		"withdrawal":                       {models.TxTypeWithdrawal, models.TxOriginUserRequest, models.TxDirectionOutbound, Kind{TypeWithdrawal, DirectionOutgoing}},
		"withdrawal ignores bad direction": {models.TxTypeWithdrawal, "", models.TxDirectionInbound, Kind{TypeWithdrawal, DirectionOutgoing}},
		"sweep":                            {models.TxTypeSweep, models.TxOriginSweep, models.TxDirectionSelf, Kind{TypeSweep, DirectionInternal}},
		"manual consolidation":             {models.TxTypeSweep, models.TxOriginManualConsolidation, models.TxDirectionSelf, Kind{TypeConsolidation, DirectionInternal}},
		"gas seed":                         {models.TxTypeGasSeed, models.TxOriginGasSeed, models.TxDirectionSelf, Kind{TypeGasFunding, DirectionInternal}},
		"fee":                              {models.TxTypeFee, "", "", Kind{TypeFee, DirectionOutgoing}},
		"inbound transfer":                 {models.TxTypeTransfer, "", models.TxDirectionInbound, Kind{TypeTransfer, DirectionIncoming}},
		"outbound token transfer":          {models.TxTypeTokenTransfer, "", models.TxDirectionOutbound, Kind{TypeTransfer, DirectionOutgoing}},
		"transfer of unknown direction":    {models.TxTypeTransfer, "", models.TxDirectionUnknown, Kind{TypeTransfer, DirectionUnknown}},
		"mixed case and spaces":            {"  DEPOSIT ", "", "", Kind{TypeDeposit, DirectionIncoming}},
		"empty":                            {"", "", "", Kind{TypeUnknown, DirectionUnknown}},
		"unrecognized type":                {"airdrop", "", models.TxDirectionInbound, Kind{TypeUnknown, DirectionIncoming}},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(tc.txType, tc.origin, tc.chainDirection); got != tc.want {
				t.Fatalf("Classify(%q, %q, %q) = %+v, want %+v", tc.txType, tc.origin, tc.chainDirection, got, tc.want)
			}
		})
	}
}
