package withdrawals_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/types"
)

func TestFailure_Codes_TheServicePersistsAreThePublicList(t *testing.T) {
	t.Parallel()

	codes := []struct{ got, want string }{
		{withdraw.FailureInsufficientFunds, resources.CodeInsufficientFunds},
		{withdraw.FailureWalletNotGasReady, resources.CodeWalletNotGasReady},
		{withdraw.FailureUnsupportedChain, resources.CodeUnsupportedChain},
		{withdraw.FailureSweepLimitExceeded, resources.CodeSweepLimitExceeded},
		{withdraw.FailureInvalidPassphrase, resources.CodeInvalidPassphrase},
		{withdraw.FailurePassphraseTooShort, resources.CodePassphraseTooShort},
		{withdraw.FailureConcurrentWithdrawal, resources.CodeConcurrentWithdrawal},
		{withdraw.FailureTooManyAttempts, resources.CodeTooManyAttempts},
		{withdraw.FailureSpendingLimit, resources.CodeSpendingLimitExceeded},
		{withdraw.FailureSpendingLimitInvalid, resources.CodeSpendingLimitInvalid},
		{withdraw.FailureSpendingQuote, resources.CodeSpendingLimitQuoteUnavailable},
		{withdraw.FailureInternalError, resources.CodeInternalError},
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

func TestLookup_Keeps_TheExternalWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	wallet := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	reason := "insufficient_funds"
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	row := &models.Withdrawal{ID: id, WalletID: wallet, Status: "failed", Amount: "4", DestinationAddress: "bc1q", FailureReason: &reason}
	row.CreatedAt = created
	row.UpdatedAt = created

	raw, err := json.Marshal(withdrawals.NewLookup(row, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"11111111-1111-4111-8111-111111111111","idempotency_key":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","status":"failed","amount":"4","destination_address":"bc1q","tx_hash":null,"transaction_status":null,"failure_reason":"insufficient_funds","created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:09"}`
	if string(raw) != want {
		t.Fatalf("wire = %s", raw)
	}

	row.Status = "broadcast"
	row.FailureReason = nil
	raw, err = json.Marshal(withdrawals.NewLookup(row, &models.Transaction{TxHash: "0xhash", Status: "confirming"}))
	if err != nil {
		t.Fatal(err)
	}
	want = `{"id":"11111111-1111-4111-8111-111111111111","idempotency_key":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","status":"broadcast","amount":"4","destination_address":"bc1q","tx_hash":"0xhash","transaction_status":"confirming","failure_reason":null,"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:09"}`
	if string(raw) != want {
		t.Fatalf("wire = %s", raw)
	}

	raw, err = json.Marshal(withdrawals.NewLookup(row, &models.Transaction{Status: "pending"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tx_hash":null`) || !strings.Contains(string(raw), `"transaction_status":"pending"`) {
		t.Fatalf("a transaction without a hash: %s", raw)
	}
}

func TestFeeEstimate_Keeps_TheTypesWire(t *testing.T) {
	t.Parallel()

	for _, estimate := range []types.FeeEstimate{
		{Fee: "0.00021", FeeAsset: "ETH"},
		{Fee: "0.00021", FeeAsset: "ETH", GasPrice: "7", GasLimit: 21000},
	} {
		want, err := json.Marshal(estimate)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(withdrawals.NewFeeEstimate(&estimate))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("wire = %s, want %s", got, want)
		}
	}
}
