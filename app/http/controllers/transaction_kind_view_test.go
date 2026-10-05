package controllers

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/txkind"
)

func decodeJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTransactionViewsCarryAnUnsignedAmountWithTypeAndDirection(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		tx                           models.Transaction
		wantType, wantDirection      string
		wantChainDirection, wantTxTy string
	}{
		"5 SOL deposit": {
			tx:       models.Transaction{Chain: "sol", Asset: "sol", Amount: "5000000000", TxType: models.TxTypeDeposit, Direction: models.TxDirectionInbound},
			wantType: txkind.TypeDeposit, wantDirection: txkind.DirectionIncoming, wantChainDirection: models.TxDirectionInbound, wantTxTy: models.TxTypeDeposit,
		},
		"0.02 SOL withdrawal": {
			tx:       models.Transaction{Chain: "sol", Asset: "sol", Amount: "20000000", TxType: models.TxTypeWithdrawal, Direction: models.TxDirectionOutbound},
			wantType: txkind.TypeWithdrawal, wantDirection: txkind.DirectionOutgoing, wantChainDirection: models.TxDirectionOutbound, wantTxTy: models.TxTypeWithdrawal,
		},
		"manual consolidation": {
			tx:       models.Transaction{Chain: "eth", Asset: "ETH", Amount: "1954853353616000", TxType: models.TxTypeSweep, Origin: models.TxOriginManualConsolidation, Direction: models.TxDirectionSelf},
			wantType: txkind.TypeConsolidation, wantDirection: txkind.DirectionInternal, wantChainDirection: models.TxDirectionSelf, wantTxTy: models.TxTypeSweep,
		},
		"gas seed": {
			tx:       models.Transaction{Chain: "polygon", Asset: "POL", Amount: "10000000000000000", TxType: models.TxTypeGasSeed, Origin: models.TxOriginGasSeed, Direction: models.TxDirectionSelf},
			wantType: txkind.TypeGasFunding, wantDirection: txkind.DirectionInternal, wantChainDirection: models.TxDirectionSelf, wantTxTy: models.TxTypeGasSeed,
		},
		"deposit without stored direction": {
			tx:       models.Transaction{Chain: "eth", Asset: "eth", Amount: "1", TxType: models.TxTypeDeposit},
			wantType: txkind.TypeDeposit, wantDirection: txkind.DirectionIncoming, wantTxTy: models.TxTypeDeposit,
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bodies := map[string]map[string]any{
				"wallet view": decodeJSON(t, walletTransactionViews([]models.Transaction{tc.tx}, polygonCatalog())[0]),
			}
			for view, body := range bodies {
				if body["amount"] != tc.tx.Amount {
					t.Errorf("%s amount = %v, want the unsigned %s", view, body["amount"], tc.tx.Amount)
				}
				if body["type"] != tc.wantType || body["direction"] != tc.wantDirection || body["tx_type"] != tc.wantTxTy {
					t.Errorf("%s type/direction/tx_type = %v/%v/%v, want %s/%s/%s",
						view, body["type"], body["direction"], body["tx_type"], tc.wantType, tc.wantDirection, tc.wantTxTy)
				}
				chainDirection, present := body["chain_direction"]
				if tc.wantChainDirection == "" && present {
					t.Errorf("%s chain_direction = %v, want it omitted", view, chainDirection)
				}
				if tc.wantChainDirection != "" && chainDirection != tc.wantChainDirection {
					t.Errorf("%s chain_direction = %v, want %s", view, chainDirection, tc.wantChainDirection)
				}
			}
		})
	}
}

func TestTransactionViewsListKeepsOrderAndLength(t *testing.T) {
	t.Parallel()

	if got := walletTransactionViews(nil, polygonCatalog()); got != nil {
		t.Fatal("nil wallet page became an empty slice")
	}
	if got := walletTransactionViews([]models.Transaction{}, polygonCatalog()); got == nil || len(got) != 0 {
		t.Fatalf("empty wallet page = %#v", got)
	}
}
