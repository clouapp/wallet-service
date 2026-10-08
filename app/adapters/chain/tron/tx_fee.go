package tron

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

// TransactionFee is the sun burned (energy + bandwidth + account activation).
// It is 0 when free bandwidth and staked energy covered the transaction.

type tronFeeInfo struct {
	ID      string `json:"id"`
	Fee     int64  `json:"fee"`
	Receipt struct {
		EnergyFee int64 `json:"energy_fee"`
		NetFee    int64 `json:"net_fee"`
	} `json:"receipt"`
}

func (a *TronLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	txID, err := normalizeTronTxID(txHash)
	if err != nil {
		return nil, err
	}
	var info tronFeeInfo
	if err := a.post(ctx, "/wallet/gettransactioninfobyid", map[string]any{"value": txID}, &info); err != nil {
		return nil, err
	}
	if info.ID == "" {
		return nil, fmt.Errorf("tron transaction %s: %w", txID, chain.ErrTransactionFeeUnknown)
	}
	if !strings.EqualFold(info.ID, txID) {
		return nil, fmt.Errorf("tron gettransactioninfobyid %s answered for %s", txID, info.ID)
	}
	fee := max(info.Fee, info.Receipt.EnergyFee+info.Receipt.NetFee)
	if fee < 0 {
		return nil, fmt.Errorf("tron transaction %s reports a negative fee %d", txID, fee)
	}
	return big.NewInt(fee), nil
}
