package bridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func achRegistration() ramp.BankAccountRegistration {
	return ramp.BankAccountRegistration{
		OwnerName:      "Ada Lovelace",
		OwnerFirstName: "Ada",
		OwnerLastName:  "Lovelace",
		BankName:       "Lead Bank",
		AccountName:    "Minha conta corrente",
		RoutingNumber:  "021000021",
		AccountNumber:  "000123456789",
		AccountType:    "checking",
		Address: ramp.BankAccountAddress{
			StreetLine1: "923 Folsom Street",
			StreetLine2: "Suite 302",
			City:        "San Francisco",
			State:       "CA",
			PostalCode:  "94107",
			Country:     "USA",
		},
		IdempotencyKey: "bank:7:12",
	}
}

// The body is the documented one, field for field: an individual carries
// first_name and last_name beside account_owner_name, and every US account an
// address.
func TestRegisterBankAccountSendsTheDocumentedBody(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": string(readFixture(t, "bank_external_account_ach_response.json")),
	})

	registered, err := adapter.RegisterBankAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, achRegistration())
	require.NoError(t, err)

	require.Len(t, *calls, 1, "a bank account is registered without a listing: Bridge masks the numbers")
	sent, err := json.Marshal((*calls)[0].body)
	require.NoError(t, err)
	require.JSONEq(t, string(readFixture(t, "bank_external_account_ach_request.json")), string(sent))
	require.Equal(t, "bank:7:12", (*calls)[0].header.Get("Idempotency-Key"))

	require.Equal(t, "039c270f-a038-40be-be7b-43f550ce0678", registered.ExternalAccountID)
	require.Equal(t, []string{"ach", "ach_same_day"}, registered.PaymentRails)
	require.NotEmpty(t, registered.Raw)
}

// payment_rails is documented only in the FedNow section; an answer without it
// says nothing about the rails, so it refuses none.
func TestRegisterBankAccountWithoutPaymentRailsRefusesNothing(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": string(readFixture(t, "bank_external_account_wire_response.json")),
	})

	req := achRegistration()
	req.OwnerName, req.OwnerFirstName, req.OwnerLastName = "Ada King Lovelace", "Ada King", "Lovelace"
	req.AccountName = "Poupança"
	req.AccountType = "savings"
	req.Address.StreetLine2 = ""

	registered, err := adapter.RegisterBankAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)

	sent, err := json.Marshal((*calls)[0].body)
	require.NoError(t, err)
	require.JSONEq(t, string(readFixture(t, "bank_external_account_wire_request.json")), string(sent))
	require.Equal(t, "039c270f-a038-40be-be7b-43f550ce0679", registered.ExternalAccountID)
	require.Nil(t, registered.PaymentRails)
}

// Without a caller's key the key is derived from the numbers, hashed: the
// Idempotency-Key header is logged by proxies, and an account number is not
// something to hand them.
func TestRegisterBankAccountDerivesAKeyWithoutTheNumbers(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": string(readFixture(t, "bank_external_account_ach_response.json")),
	})

	req := achRegistration()
	req.IdempotencyKey = ""
	_, err := adapter.RegisterBankAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)

	key := (*calls)[0].header.Get("Idempotency-Key")
	require.Regexp(t, `^ext-acct-us:cus_1:[0-9a-f]{16}$`, key)
	require.NotContains(t, key, "000123456789")
	require.NotContains(t, key, "021000021")
}

func TestRegisterBankAccountRefusesWithoutACustomer(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})

	_, err := adapter.RegisterBankAccount(context.Background(), ramp.CustomerRef{CustomerID: 7}, achRegistration())
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	require.Empty(t, *calls)
}

func TestRegisterBankAccountRefusesAnAnswerWithoutAnID(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": `{"currency":"usd"}`,
	})

	_, err := adapter.RegisterBankAccount(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, achRegistration())
	require.EqualError(t, err, "bridge: external account created without an id")
}

// Deleting a bank account deactivates its external account: Bridge has no
// deactivate for a liquidation address, and a deactivated external account is
// what stops new payouts to it. The call carries no body and a key per
// account, so a retry is the same request.
func TestDeactivateBankAccountDeactivatesTheExternalAccount(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts/ext_1/deactivate": `{"id":"ext_1","active":false}`,
	})

	err := adapter.DeactivateBankAccount(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "ext_1")
	require.NoError(t, err)
	require.Len(t, *calls, 1)
	require.Nil(t, (*calls)[0].body)
	require.Equal(t, "ext-deactivate:ext_1", (*calls)[0].header.Get("Idempotency-Key"))
}

func TestDeactivateBankAccountNeedsTheCustomer(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})

	err := adapter.DeactivateBankAccount(context.Background(), ramp.CustomerRef{CustomerID: 7}, "ext_1")
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	require.Empty(t, *calls)
}
