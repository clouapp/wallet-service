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

// splTransferMeta is the meta of an SPL transfer from the payer (index 0): account 1
// is the recipient's token account and account 2 the payer's.
func splTransferMeta(recipientExisted bool) string {
	preRecipient, preTokens := "0", `[{"accountIndex":2,"mint":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","uiTokenAmount":{"amount":"5000000"}}]`
	if recipientExisted {
		preRecipient = "2039280"
		preTokens = `[{"accountIndex":1,"mint":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","uiTokenAmount":{"amount":"0"}},{"accountIndex":2,"mint":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","uiTokenAmount":{"amount":"5000000"}}]`
	}
	payerAfter := "99995000"
	if !recipientExisted {
		payerAfter = "97955720"
	}
	return `"result":{"slot":1,"meta":{"fee":5000,"err":null,` +
		`"preBalances":[100000000,` + preRecipient + `,2039280,1,934087680],` +
		`"postBalances":[` + payerAfter + `,2039280,2039280,1,934087680],` +
		`"preTokenBalances":` + preTokens + `,` +
		`"postTokenBalances":[{"accountIndex":1,"mint":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","uiTokenAmount":{"amount":"1000000"}},{"accountIndex":2,"mint":"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU","uiTokenAmount":{"amount":"4000000"}}]}}`
}

func TestSolana_TransactionAccountRent_IsTheRentOfTheTokenAccountsTheTransferCreated(t *testing.T) {
	for name, tc := range map[string]struct {
		result string
		want   int64
	}{
		"SPL transfer that created the recipient's token account": {splTransferMeta(false), 2039280},
		"SPL transfer to an existing token account":               {splTransferMeta(true), 0},
		"SOL transfer to a new system account": {
			`"result":{"slot":1,"meta":{"fee":5000,"err":null,"preBalances":[100000000,0,1],"postBalances":[89995000,10000000,1],"preTokenBalances":[],"postTokenBalances":[]}}`, 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := fakeSolanaRPC(t, map[string]string{"getTransaction": tc.result})
			rent, err := adapter.TransactionAccountRent(context.Background(), "5sig")
			if err != nil || rent.Int64() != tc.want {
				t.Fatalf("rent = %v, %v; want %d", rent, err, tc.want)
			}
		})
	}
	missing := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":null`})
	if _, err := missing.TransactionAccountRent(context.Background(), "5sig"); !errors.Is(err, chain.ErrTransactionFeeUnknown) {
		t.Fatalf("missing tx err = %v", err)
	}
	unbalanced := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":{"slot":1,"meta":{"fee":5000,"preBalances":[1],"postBalances":[1,2],"preTokenBalances":[],"postTokenBalances":[{"accountIndex":1}]}}`})
	if _, err := unbalanced.TransactionAccountRent(context.Background(), "5sig"); err == nil {
		t.Fatal("balances that do not line up with the keys must fail, not read as no rent")
	}
}
