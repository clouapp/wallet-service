package withdrawals_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/models"
)

func TestFailure_Codes_AreThePublicList(t *testing.T) {
	t.Parallel()

	codes := []struct{ got, want string }{
		{withdrawals.FailureInsufficientFunds, resources.CodeInsufficientFunds},
		{withdrawals.FailureWalletNotGasReady, resources.CodeWalletNotGasReady},
		{withdrawals.FailureUnsupportedChain, resources.CodeUnsupportedChain},
		{withdrawals.FailureSweepLimitExceeded, resources.CodeSweepLimitExceeded},
		{withdrawals.FailureInvalidPassphrase, resources.CodeInvalidPassphrase},
		{withdrawals.FailurePassphraseTooShort, resources.CodePassphraseTooShort},
		{withdrawals.FailureConcurrentWithdrawal, resources.CodeConcurrentWithdrawal},
		{withdrawals.FailureTooManyAttempts, resources.CodeTooManyAttempts},
		{withdrawals.FailureSpendingLimit, resources.CodeSpendingLimitExceeded},
		{withdrawals.FailureSpendingLimitInvalid, resources.CodeSpendingLimitInvalid},
		{withdrawals.FailureSpendingQuote, resources.CodeSpendingLimitQuoteUnavailable},
		{withdrawals.FailureInternalError, resources.CodeInternalError},
	}
	seen := map[string]struct{}{}
	for _, code := range codes {
		if code.got != code.want || code.got == "" {
			t.Fatalf("failure code %q is not the public list value %q", code.got, code.want)
		}
		seen[code.got] = struct{}{}
	}
	if len(seen) != len(codes) {
		t.Fatalf("duplicate failure codes: %d unique, %d declared", len(seen), len(codes))
	}
}

func TestWithdrawal_Keeps_TheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	reason := "insufficient_funds"
	full := models.Withdrawal{
		ID: id, WalletID: other, TransactionID: &id, AccountID: &other, Status: "broadcast",
		Amount: "1.5", DestinationAddress: "bc1q", FeeEstimate: "0.1", Note: "hi",
		CreatedBy: &id, FailureReason: &reason, TxHash: "0xhash",
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		withdrawal models.Withdrawal
		want       string
	}{
		{
			withdrawal: models.Withdrawal{},
			want:       `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","status":"","amount":"","destination_address":""}`,
		},
		{
			withdrawal: models.Withdrawal{Status: "pending", Amount: "0"},
			want:       `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","status":"pending","amount":"0","destination_address":""}`,
		},
		{
			withdrawal: full,
			want:       `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","transaction_id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","status":"broadcast","amount":"1.5","destination_address":"bc1q","fee_estimate":"0.1","note":"hi","created_by":"11111111-1111-4111-8111-111111111111","failure_reason":"insufficient_funds","tx_hash":"0xhash"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(withdrawals.WithdrawalFrom(tc.withdrawal))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}

	nilRaw, err := json.Marshal(withdrawals.WithdrawalPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil withdrawal = %s", nilRaw)
	}
}

func TestWithdrawals_Preserve_SliceNilness(t *testing.T) {
	t.Parallel()

	if withdrawals.WithdrawalsFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := withdrawals.WithdrawalsFrom([]models.Withdrawal{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(pagination.Response(withdrawals.WithdrawalsFrom(nil), 0, 50, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null,"limit":50,"offset":0,"total":0}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(pagination.Response(withdrawals.WithdrawalsFrom([]models.Withdrawal{}), 0, 50, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[],"limit":50,"offset":0,"total":0}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
