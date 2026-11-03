package ramp

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func validFundingAction() FundingAction {
	return FundingAction{
		Type:           FundingActionSolanaTransaction,
		SubmissionMode: SubmissionBackendFinalize,
		Payload: FundingActionPayload{
			ContentType:         ContentTypeSolanaTransaction,
			UnsignedTransaction: base64.StdEncoding.EncodeToString([]byte("tx")),
		},
		ExpiresAt: time.Now().Add(time.Minute),
	}
}

func TestFundingActionValidate(t *testing.T) {
	big := func(n int) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, n)) }
	cases := []struct {
		name   string
		mutate func(*FundingAction)
		ok     bool
	}{
		{"valid", func(*FundingAction) {}, true},
		{"max size", func(a *FundingAction) { a.Payload.UnsignedTransaction = big(MaxUnsignedTransactionBytes) }, true},
		{"type", func(a *FundingAction) { a.Type = "evm_transaction" }, false},
		{"mode", func(a *FundingAction) { a.SubmissionMode = "client_submit" }, false},
		{"content type", func(a *FundingAction) { a.Payload.ContentType = "text/plain" }, false},
		{"invalid base64", func(a *FundingAction) { a.Payload.UnsignedTransaction = "not base64!" }, false},
		{"empty", func(a *FundingAction) { a.Payload.UnsignedTransaction = "" }, false},
		{"oversized", func(a *FundingAction) { a.Payload.UnsignedTransaction = big(MaxUnsignedTransactionBytes + 1) }, false},
		{"zero expiry", func(a *FundingAction) { a.ExpiresAt = time.Time{} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := validFundingAction()
			tc.mutate(&a)
			if tc.ok {
				assert.NoError(t, a.Validate())
			} else {
				assert.Error(t, a.Validate())
			}
		})
	}
}
