package solana

import (
	"context"
	"errors"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
)

func TestSolanaTransactionFee(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":{"slot":1,"meta":{"fee":5000,"err":null}}`})
	fee, err := adapter.TransactionFee(context.Background(), "5sig")
	if err != nil || fee.Int64() != 5000 {
		t.Fatalf("fee = %v, %v", fee, err)
	}
	missing := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":null`})
	if _, err := missing.TransactionFee(context.Background(), "5sig"); !errors.Is(err, chain.ErrTransactionFeeUnknown) {
		t.Fatalf("missing tx err = %v", err)
	}
}
