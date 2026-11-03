package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// solanaAddress is deliberately mixed case. Base58 is case-sensitive, so an
// adapter that lowercased addresses the way it lowercases currency codes would
// send the money somewhere that does not exist.
const solanaAddress = "8nWZBmoDLpTgemBqfF8pQUYAKxJBvpTgemBq"

// brlVirtualAccountBody is a provisioned account as Bridge answers it: the
// status in its own vocabulary, and the permanent, amount-less br_code under
// source_deposit_instructions.
const brlVirtualAccountBody = `{
	"id":"va_1",
	"status":"activated",
	"customer_id":"cus_1",
	"developer_fee_percent":"1.50",
	"source_deposit_instructions":{
		"currency":"brl",
		"br_code":"00020126770014br.gov.bcb.pix0136d7c1cf5e630416E9",
		"account_holder_name":"Edson Arantes do Nascimento",
		"payment_rails":["pix"]
	},
	"destination":{
		"currency":"usdc",
		"payment_rail":"solana",
		"address":"` + solanaAddress + `"
	}
}`

func brlAccountRequest() ramp.VirtualAccountRequest {
	return ramp.VirtualAccountRequest{
		// Uppercase as the client API writes a currency for a user; Bridge only
		// takes lowercase.
		SourceCurrency: "BRL",
		Destination: ramp.CryptoLeg{
			Currency:    "USDC",
			PaymentRail: "solana",
			Address:     solanaAddress,
		},
		FeePercent: "1.50",
	}
}

// listBody wraps objects the way Bridge's collection endpoints answer.
func listBody(items ...string) string {
	return `{"count":` + strconv.Itoa(len(items)) + `,"data":[` + strings.Join(items, ",") + `]}`
}

// --- provisioning -----------------------------------------------------------

// Bridge refuses a second registration of the same PIX key (400
// duplicate_external_account in sandbox, 422 in production); virtual accounts
// are not deduplicated at all. A repeat POST under a fresh idempotency key
// returns a second account with its own br_code, and a deposit into the one we
// stopped tracking is money with no order.
func TestProvisionVirtualAccountListsBeforeCreating(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts":  listBody(),
		"POST /customers/cus_1/virtual_accounts": brlVirtualAccountBody,
	})

	account, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, brlAccountRequest())
	require.NoError(t, err)

	require.Len(t, *calls, 2, "the listing has to happen before the create")
	require.Equal(t, http.MethodGet, (*calls)[0].method)
	require.Contains(t, (*calls)[0].query, "limit=")
	require.Equal(t, http.MethodPost, (*calls)[1].method)

	created := (*calls)[1]
	source := created.body["source"].(map[string]any)
	require.Equal(t, "brl", source["currency"], "Bridge rejects an uppercase code outright")

	destination := created.body["destination"].(map[string]any)
	require.Equal(t, "usdc", destination["currency"])
	require.Equal(t, "solana", destination["payment_rail"], "a rail is not a currency code")
	require.Equal(t, solanaAddress, destination["address"], "base58 is case-sensitive")
	require.Equal(t, "1.50", created.body["developer_fee_percent"])

	// Derived from the currency parameter, so a retry reaches Bridge as the same
	// create rather than as a second account.
	require.Equal(t, "va:cus_1:brl", created.header.Get("Idempotency-Key"))

	require.Equal(t, "va_1", account.ProviderAccountID)
	require.Equal(t, models.VirtualAccountStatusActive, account.Status)
	require.Equal(t, "brl", account.SourceCurrency)
	require.Equal(t, "1.50", account.FeePercent)
	require.Equal(t, "00020126770014br.gov.bcb.pix0136d7c1cf5e630416E9",
		account.DepositInstructions["br_code"])
	require.Equal(t, "usdc", account.Destination.Currency)
	require.Equal(t, "solana", account.Destination.PaymentRail)
	require.Equal(t, solanaAddress, account.Destination.Address)
	require.NotEmpty(t, account.Raw)
}

// The listing is the whole point: an account that already exists is returned
// as-is and no create is attempted.
func TestProvisionVirtualAccountReturnsTheExistingAccount(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts": listBody(brlVirtualAccountBody),
	})

	account, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, brlAccountRequest())
	require.NoError(t, err)

	// A POST is not in the route table, so making one would fail the test.
	require.Len(t, *calls, 1)
	require.Equal(t, "va_1", account.ProviderAccountID)
	require.Equal(t, "brl", account.SourceCurrency)
	require.NotEmpty(t, account.Raw, "the listed element is the raw payload")
}

// Neither a deactivated account nor one collecting another currency is the
// account being asked for. A deactivated one returns funds to the sender, so
// handing it back as "your deposit account" would be worse than creating one.
func TestProvisionVirtualAccountSkipsInactiveAndOtherCurrencies(t *testing.T) {
	deactivated := `{"id":"va_old","status":"deactivated","source_deposit_instructions":{"currency":"brl"}}`
	otherCurrency := `{"id":"va_usd","status":"activated","source_deposit_instructions":{"currency":"usd"}}`

	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts":  listBody(deactivated, otherCurrency),
		"POST /customers/cus_1/virtual_accounts": brlVirtualAccountBody,
	})

	account, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, brlAccountRequest())
	require.NoError(t, err)

	require.Len(t, *calls, 2)
	require.Equal(t, "va_1", account.ProviderAccountID)
}

// An account in a state Bridge has not published yet is still an account.
//
// The canonical status collapses anything unrecognised onto `inactive`, so a
// reuse check written against `active` would skip this one and create a second
// account — which Bridge does not prevent (§15 Q5) and which leaves one user
// with two deposit addresses. Only `deactivated` is skipped; everything else
// counts as present, and no POST is made.
func TestProvisionVirtualAccountReusesAnAccountInAnUnknownState(t *testing.T) {
	unfamiliar := `{"id":"va_odd","status":"suspended","source_deposit_instructions":{"currency":"brl"}}`

	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts":  listBody(unfamiliar),
		"POST /customers/cus_1/virtual_accounts": brlVirtualAccountBody,
	})

	account, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, brlAccountRequest())
	require.NoError(t, err)

	require.Len(t, *calls, 1, "no create: the account already exists")
	require.Equal(t, "va_odd", account.ProviderAccountID)
	require.Equal(t, models.VirtualAccountStatusInactive, account.Status,
		"reused, but not claimed to be healthy")
}

// The service derives a key that survives a retry; when it supplies one the
// adapter forwards it rather than substituting its own.
func TestProvisionVirtualAccountForwardsTheSuppliedIdempotencyKey(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts":  listBody(),
		"POST /customers/cus_1/virtual_accounts": brlVirtualAccountBody,
	})

	req := brlAccountRequest()
	req.IdempotencyKey = "service-derived-key"

	_, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)
	require.Equal(t, "service-derived-key", (*calls)[1].header.Get("Idempotency-Key"))
}

func TestProvisionVirtualAccountRefusesLocally(t *testing.T) {
	adapter := unreachableAdapter(t)

	_, err := adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, brlAccountRequest())
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)

	// SOL has no rate and no route at Bridge, so an account paying out in it
	// could never settle.
	sol := brlAccountRequest()
	sol.Destination.Currency = "SOL"
	_, err = adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, sol)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)

	// Without a source currency there is nothing to collect and nothing to
	// match a listed account against.
	blank := brlAccountRequest()
	blank.SourceCurrency = ""
	_, err = adapter.ProvisionVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, blank)
	require.Error(t, err)
}

// --- read and update --------------------------------------------------------

func TestGetVirtualAccountNormalizesTheStatus(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1": `{"id":"va_1","status":"deactivated",
			"source_deposit_instructions":{"currency":"brl"},
			"destination":{"currency":"usdc","payment_rail":"solana","address":"` + solanaAddress + `"}}`,
	})

	account, err := adapter.GetVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1")
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Equal(t, models.VirtualAccountStatusInactive, account.Status)
}

func TestGetVirtualAccountRequiresAnAccountID(t *testing.T) {
	_, err := unreachableAdapter(t).GetVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "")
	require.Error(t, err)
}

// An update sends only what it was asked to change: Bridge treats the PUT as a
// patch, and restating a field would overwrite a value changed elsewhere.
func TestUpdateVirtualAccountSendsOnlyTheChangedFields(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"PUT /customers/cus_1/virtual_accounts/va_1": brlVirtualAccountBody,
	})

	newAddress := "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"
	account, err := adapter.UpdateVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", ramp.VirtualAccountUpdate{
			Destination: &ramp.CryptoLeg{Currency: "USDC", PaymentRail: "solana", Address: newAddress},
			FeePercent:  "2.50",
		})
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Equal(t, http.MethodPut, (*calls)[0].method)
	destination := (*calls)[0].body["destination"].(map[string]any)
	require.Equal(t, "usdc", destination["currency"])
	require.Equal(t, newAddress, destination["address"])
	require.Equal(t, "2.50", (*calls)[0].body["developer_fee_percent"])
	require.NotContains(t, (*calls)[0].body, "status", "an update that says nothing about activation must not change it")

	// The deposit instructions survive an update — the user has the br_code
	// saved in their bank app.
	require.Equal(t, "00020126770014br.gov.bcb.pix0136d7c1cf5e630416E9",
		account.DepositInstructions["br_code"])
}

func TestUpdateVirtualAccountDrivesActivation(t *testing.T) {
	for _, tc := range []struct {
		active bool
		path   string
	}{
		{false, "/customers/cus_1/virtual_accounts/va_1/deactivate"},
		{true, "/customers/cus_1/virtual_accounts/va_1/activate"},
	} {
		adapter, calls := routedAdapter(t, map[string]string{
			http.MethodPost + " " + tc.path: brlVirtualAccountBody,
		})

		_, err := adapter.UpdateVirtualAccount(context.Background(),
			ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1",
			ramp.VirtualAccountUpdate{Active: &tc.active})
		require.NoError(t, err)
		require.Equal(t, http.MethodPost, (*calls)[0].method)
		require.Equal(t, tc.path, (*calls)[0].path)
		require.Empty(t, (*calls)[0].body, "activation is its own endpoint, not a PUT field")
		require.NotEmpty(t, (*calls)[0].header.Get("Idempotency-Key"), "Bridge requires one on this POST")
	}
}

// Setting a status is idempotent by itself, and a key reused across calls is
// replayed: deactivate, activate, deactivate under one key would answer the
// first deactivation without performing the second, and Bridge refuses a key
// first used more than 24 hours earlier for good. Each call carries its own.
func TestUpdateVirtualAccountActivationKeyIsFreshPerCall(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/virtual_accounts/va_1/deactivate": brlVirtualAccountBody,
	})
	inactive := false
	for range 2 {
		_, err := adapter.UpdateVirtualAccount(context.Background(),
			ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1",
			ramp.VirtualAccountUpdate{Active: &inactive})
		require.NoError(t, err)
	}

	require.Len(t, *calls, 2)
	require.NotEqual(t, (*calls)[0].header.Get("Idempotency-Key"), (*calls)[1].header.Get("Idempotency-Key"))
}

// Activation is a separate endpoint from the PUT, so one update cannot carry
// both. Refusing is the honest answer: sending one call would silently drop
// what the other was asked to change.
func TestUpdateVirtualAccountRefusesActivationWithOtherChanges(t *testing.T) {
	inactive := false
	_, err := unreachableAdapter(t).UpdateVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1",
		ramp.VirtualAccountUpdate{Active: &inactive, FeePercent: "2.00"})
	require.Error(t, err)
}

func TestUpdateVirtualAccountRefusesAnUnservedDestination(t *testing.T) {
	_, err := unreachableAdapter(t).UpdateVirtualAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1",
		ramp.VirtualAccountUpdate{Destination: &ramp.CryptoLeg{Currency: "SOL", PaymentRail: "solana"}})
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// --- deposits ---------------------------------------------------------------

// activityFromTestdata returns the `event_object` of a captured delivery. The
// history endpoint answers with the same activity shape, so the parser is
// exercised against bytes Bridge actually produced rather than against our
// reading of the documentation — the discipline Wave 0 exists to enforce.
func activityFromTestdata(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	var envelope struct {
		EventObject json.RawMessage `json:"event_object"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.NotEmpty(t, envelope.EventObject)
	return envelope.EventObject
}

// withField rewrites one field of a captured activity, so a second deposit in
// the same history is still a real payload rather than a hand-written one.
func withField(t *testing.T, raw json.RawMessage, key string, value any) json.RawMessage {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	decoded[key] = value
	rewritten, err := json.Marshal(decoded)
	require.NoError(t, err)
	return rewritten
}

func historyBody(activities ...json.RawMessage) string {
	items := make([]string, 0, len(activities))
	for _, activity := range activities {
		items = append(items, string(activity))
	}
	return listBody(items...)
}

const capturedDepositID = "60d29e54-bedf-4a4b-a76e-001959f15f45"

// The captured activities carry `amount` on the fiat leg for funds_received and
// on the crypto leg for the payment events — "100.0"/"usd" against
// "97.5"/"usdc". Reading `amount` into the source amount would put 97.5 USDC
// where the reais belong, so both amounts come off the receipt.
func TestGetDepositReadsTheAmountsOffTheReceipt(t *testing.T) {
	funds := activityFromTestdata(t, "va_activity_funds_received.json")
	submitted := activityFromTestdata(t, "va_activity_payment_submitted.json")
	processed := activityFromTestdata(t, "va_activity_payment_processed.json")

	cases := []struct {
		name              string
		history           string
		status            string
		sourceAmount      string
		sourceCurrency    string
		destinationAmount string
		txHash            string
	}{
		{
			name:    "only the funds event has arrived",
			history: historyBody(funds),
			status:  models.TxStatusFundsReceived,
			// No receipt yet, so the activity's own amount is the fiat leg.
			sourceAmount:   "100.0",
			sourceCurrency: "usd",
		},
		{
			name:              "the payment is in flight",
			history:           historyBody(funds, submitted),
			status:            models.TxStatusProcessing,
			sourceAmount:      "100.0",
			sourceCurrency:    "usd",
			destinationAmount: "97.5",
			txHash:            "0x717fa898a4ec3f8050cb0211f381b1ab9b4a7ebb04f81da26884ff846f78652c",
		},
		{
			name:              "the payment settled",
			history:           historyBody(funds, submitted, processed),
			status:            models.TxStatusCompleted,
			sourceAmount:      "100.0",
			sourceCurrency:    "usd",
			destinationAmount: "97.5",
			txHash:            "0x717fa898a4ec3f8050cb0211f381b1ab9b4a7ebb04f81da26884ff846f78652c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, calls := routedAdapter(t, map[string]string{
				"GET /customers/cus_1/virtual_accounts/va_1/history": tc.history,
			})

			deposit, err := adapter.GetDeposit(context.Background(),
				ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", capturedDepositID)
			require.NoError(t, err)

			require.Len(t, *calls, 1)
			require.Contains(t, (*calls)[0].query, "limit=")

			require.Equal(t, capturedDepositID, deposit.DepositID)
			require.Equal(t, "337c390b-ba2e-44c7-a916-40aff905d897", deposit.ProviderAccountID)
			require.Equal(t, tc.status, deposit.Status)
			require.Equal(t, tc.sourceAmount, deposit.SourceAmount)
			require.Equal(t, tc.sourceCurrency, deposit.SourceCurrency,
				"the fiat currency is never read off a payment event")
			require.Equal(t, tc.destinationAmount, deposit.DestinationAmount)
			require.Equal(t, tc.txHash, deposit.TxHash)

			// The fee is known from the first event and is quoted in the fiat
			// the user sent, because it comes off before the conversion.
			require.Equal(t, "2.5", deposit.FeeAmount)
			require.Equal(t, tc.sourceCurrency, deposit.FeeCurrency)
			require.Equal(t, "0.0", deposit.ExchangeFeeAmount)
			require.NotNil(t, deposit.OccurredAt)
			require.NotEmpty(t, deposit.Raw)
		})
	}
}

// Bridge has no single-deposit read, so the history is filtered here — and the
// answer must not depend on the order the page happens to arrive in.
func TestGetDepositFiltersByDepositIDInEitherOrder(t *testing.T) {
	funds := activityFromTestdata(t, "va_activity_funds_received.json")
	processed := activityFromTestdata(t, "va_activity_payment_processed.json")
	// Another user's money in the same account's history.
	otherDeposit := withField(t, processed, "deposit_id", "other-deposit")
	otherDeposit = withField(t, otherDeposit, "amount", "500.0")

	for _, order := range []string{"newest first", "oldest first"} {
		t.Run(order, func(t *testing.T) {
			body := historyBody(processed, funds, otherDeposit)
			if order == "oldest first" {
				body = historyBody(otherDeposit, funds, processed)
			}
			adapter, _ := routedAdapter(t, map[string]string{
				"GET /customers/cus_1/virtual_accounts/va_1/history": body,
			})

			deposit, err := adapter.GetDeposit(context.Background(),
				ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", capturedDepositID)
			require.NoError(t, err)

			require.Equal(t, capturedDepositID, deposit.DepositID)
			require.Equal(t, models.TxStatusCompleted, deposit.Status,
				"the most advanced state of the deposit wins, whatever order the page came in")
			require.Equal(t, "100.0", deposit.SourceAmount)
		})
	}
}

// The payer is the only handle an operator has on a third-party deposit, and
// `source` is rail-shaped: a wire names originator_name, PIX names
// sender_name (documented, unverified). An unrecognised rail leaves it empty
// rather than guessing.
func TestGetDepositReadsThePayerFromTheRail(t *testing.T) {
	funds := activityFromTestdata(t, "va_activity_funds_received.json")

	adapter, _ := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(funds),
	})
	deposit, err := adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", capturedDepositID)
	require.NoError(t, err)
	require.Equal(t, "Sandbox Test Customer", deposit.SenderName)
	require.Equal(t, "Sandbox deposit simulation", deposit.SenderReference)

	// The PIX field names are Bridge's documented ones and still unverified —
	// every captured fixture is a wire. This does not prove the guess; it
	// pins it so a real BRL capture later fails here instead of silently
	// persisting the wrong handle. Recapture when the BRL entitlement lands
	// (plan Task 1: "request BRL Virtual Account entitlement").
	pix := withField(t, funds, "source", map[string]any{
		"payment_rail": "pix",
		"sender_name":  "Maria Silva",
		"reference":    "E00000000202608071200",
	})
	adapter, _ = routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(pix),
	})
	deposit, err = adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", capturedDepositID)
	require.NoError(t, err)
	require.Equal(t, "Maria Silva", deposit.SenderName)
	require.Equal(t, "E00000000202608071200", deposit.SenderReference)

	unknownRail := withField(t, funds, "source", map[string]any{
		"payment_rail": "some_new_rail", "originator_name": "Someone",
	})
	adapter, _ = routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(unknownRail),
	})
	deposit, err = adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", capturedDepositID)
	require.NoError(t, err)
	require.Empty(t, deposit.SenderName, "a rail we cannot read must not have its payer guessed at")
}

// A deposit the history does not carry is not an empty deposit: reporting one
// would let the reconciler blank an order it could not actually read.
func TestGetDepositUnknownDepositIsAnError(t *testing.T) {
	funds := activityFromTestdata(t, "va_activity_funds_received.json")
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(funds),
	})

	_, err := adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", "no-such-deposit")
	require.Error(t, err)
}

func TestGetDepositRequiresItsIdentifiers(t *testing.T) {
	adapter := unreachableAdapter(t)

	_, err := adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", "")
	require.Error(t, err)

	_, err = adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "", capturedDepositID)
	require.Error(t, err)
}

// --- the rail ---------------------------------------------------------------

func TestDepositStatusFromBridge(t *testing.T) {
	cases := map[string]string{
		"funds_received":    models.TxStatusFundsReceived,
		"funds_scheduled":   models.TxStatusFundsReceived,
		"in_review":         models.TxStatusProcessing,
		"payment_submitted": models.TxStatusProcessing,
		// The money came in and is going back: refunded is terminal and would
		// claim the user already has it.
		"refund_in_flight":  models.TxStatusProcessing,
		"payment_processed": models.TxStatusCompleted,
		"refunded":          models.TxStatusRefunded,
		// The webhook examples' spellings of funds_received and refunded.
		"funding": models.TxStatusFundsReceived,
		"refund":  models.TxStatusRefunded,
		// Terminal and negative — the money is stuck and needs support.
		"refund_failed": models.TxStatusFailed,
		// An unknown state must not be terminal and must never be completed.
		"something_new": models.TxStatusProcessing,
		"":              models.TxStatusProcessing,
	}
	for activityType, expected := range cases {
		require.Equal(t, expected, depositStatusFromBridge(activityType), "type %q", activityType)
	}
}

// A failed listing left the account uncreated, so the service may release its
// reservation; a failed create may not, since Bridge may hold the account.
func TestProvisionVirtualAccountReportsWhetherTheCreateWasSent(t *testing.T) {
	for failing, notSent := range map[string]bool{
		"GET /customers/cus_1/virtual_accounts":  true,
		"POST /customers/cus_1/virtual_accounts": false,
	} {
		t.Run(failing, func(t *testing.T) {
			routes := map[string]string{
				"GET /customers/cus_1/virtual_accounts":  listBody(),
				"POST /customers/cus_1/virtual_accounts": brlVirtualAccountBody,
			}
			routes[failing] = `not json`
			adapter, calls := routedAdapter(t, routes)

			_, err := adapter.ProvisionVirtualAccount(context.Background(),
				ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, brlAccountRequest())
			require.Error(t, err)
			require.Equal(t, notSent, errors.Is(err, ramp.ErrRequestNotSent))
			if notSent {
				require.Len(t, *calls, 1, "no create after a failed listing")
			}
		})
	}
}

// An ACH deposit names its payer as sender_name and its memo as description
// (virtual-account-events, 2026-10-14), and reports the rail it came on.
func TestGetDepositReadsAnACHPayerAndItsRail(t *testing.T) {
	ach := activityFromTestdata(t, "va_activity_ach_funds_received.json")
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(ach),
	})

	deposit, err := adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", "deposit_ach_123")
	require.NoError(t, err)
	require.Equal(t, "Acme Payroll LLC", deposit.SenderName)
	require.Equal(t, "PAYROLL OCT", deposit.SenderReference)
	require.Equal(t, "ach", deposit.PaymentRail)
	require.Equal(t, "120.0", deposit.SourceAmount)
	require.Equal(t, "usd", deposit.SourceCurrency)

	fednow := withField(t, ach, "source", map[string]any{
		"payment_rail": "fednow", "sender_name": "Jane Roe", "description": "rent",
	})
	adapter, _ = routedAdapter(t, map[string]string{
		"GET /customers/cus_1/virtual_accounts/va_1/history": historyBody(fednow),
	})
	deposit, err = adapter.GetDeposit(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "va_1", "deposit_ach_123")
	require.NoError(t, err)
	require.Equal(t, "Jane Roe", deposit.SenderName)
	require.Equal(t, "fednow", deposit.PaymentRail)
}
