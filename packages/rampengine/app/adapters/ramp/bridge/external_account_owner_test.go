package bridge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// A CNPJ key belongs to a company, so the external account Bridge registers
// for it is a business's: account_owner_type business, with the recipient's
// name as business_name and as account_owner_name, which the sandbox refuses
// a business account without (400, 18/10/2026). Every other key type stays an
// individual's, and the idempotency key is the one sent before (spec §19.3):
// the owner type is not part of it.
func TestExternalAccountOwnerTypeFollowsTheKeyType(t *testing.T) {
	cases := []struct {
		keyType, key, document, owner string
	}{
		{"cnpj", "12ABC34501DE35", "", "business"},
		{"cnpj", "11222333000181", "", "business"},
		{"cpf", "39053344705", "", "individual"},
		{"email", "luiza@example.com", "39053344705", "individual"},
		{"phone", "+5511999999999", "11222333000181", "individual"},
		{"evp", "550e8400-e29b-41d4-a716-446655440000", "39053344705", "individual"},
	}
	for _, c := range cases {
		t.Run(c.keyType+" "+c.key, func(t *testing.T) {
			body, idempotencyKey := registerExternalAccount(t, c.keyType, c.key, c.document)

			require.Equal(t, c.owner, body["account_owner_type"])
			require.Equal(t, "Luiza Souza", body["account_owner_name"])
			if c.owner == "business" {
				require.Equal(t, "Luiza Souza", body["business_name"])
			} else {
				require.NotContains(t, body, "business_name")
			}
			document := c.document
			if document == "" {
				document = c.key
			}
			require.Equal(t, "ext-acct:cus_1:"+c.key+":"+document, idempotencyKey)
		})
	}
}

// registerExternalAccount runs one off-ramp create to a key and answers the
// external account body and Idempotency-Key it sent.
func registerExternalAccount(t *testing.T, keyType, key, document string) (map[string]any, string) {
	t.Helper()
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": `{"id":"ext_1"}`,
		"POST /transfers":                         offrampTransferBody,
	})
	req := offrampQuote()
	req.Destination.PixKey, req.Destination.PixKeyType, req.Destination.RecipientDocument = key, keyType, document
	_, err := adapter.CreateOfframp(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: req, IdempotencyKey: "offramp-" + keyType})
	require.NoError(t, err)
	require.NotEmpty(t, *calls)
	registered := (*calls)[0]
	require.Equal(t, "/customers/cus_1/external_accounts", registered.path)
	return registered.body, registered.header.Get("Idempotency-Key")
}
