package ozav

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const payoutDestinationBody = `{
	"id":"ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT","object":"payout_destination",
	"customer_id":"ozav_cus_1","currency":"BRL","rail":"pix","status":"active",
	"capabilities":{"receive":false}
}`

// offrampTransferBody is the shape a completed payout answers with, from the
// 2026-08-27 capture of the USD/SWIFT leg — the only direction that has ever
// completed — with the BRL/Pix legs substituted.
const offrampTransferBody = `{
	"id":"ozav_tr_01M125C6Z9QBKP4T03H43K94FP","object":"transfer",
	"customer_id":"ozav_cus_1","quote_id":"ozav_q_1","status":"payment_confirmed",
	"source":{"type":"wallet","wallet_id":"ozav_wal_1","currency":"USDC"},
	"destination":{"type":"payout_destination","payout_destination_id":"ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT"},
	"amounts":{"source":{"amount":"100","currency":"USDC"},
		"target":{"amount":"543.90","currency":"BRL"},"rate":"5.439000"},
	"created_at":"2026-08-31T15:18:23Z"
}`

// provisioningRequiredBody is the refusal every BRL/Pix payout has met in
// sandbox. Ozav documents it as retryable: a destination is `active` the
// moment it is created but unusable until registration with the settlement
// rail finishes, and no field distinguishes the two states.
const provisioningRequiredBody = `{"error":{"type":"invalid_request_error",
	"code":"PROVISIONING_REQUIRED","param":"destination",
	"message":"This payout destination is not yet enabled for transfers. Complete destination registration first.",
	"request_id":"9dc02427-e6b2-40bf-ad3a-5ba6f280998d"}}`

func offrampCreate() ramp.OfframpCreateRequest {
	return ramp.OfframpCreateRequest{
		OfframpQuoteRequest: offrampQuote(),
		ProviderQuoteID:     "ozav_q_1",
		IdempotencyKey:      "offramp-0193f2ac8d5e7b21",
	}
}

func TestCreateOfframpRegistersThePixKeyAsADestination(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)

	registered := fake.call(http.MethodPost, "/payout-destinations")
	require.NotNil(t, registered, "the Pix key must be registered before it can be paid")
	var body map[string]any
	require.NoError(t, json.Unmarshal(registered.body, &body))

	require.Equal(t, "ozav_cus_1", body["customer_id"])
	require.Equal(t, "BRL", body["currency"], "uppercase is the canonical form")
	require.Equal(t, "pix", body["rail"])

	holder, ok := body["account_holder"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Luiza Souza", holder["name"])
	require.Equal(t, "individual", holder["type"])

	details, ok := body["details"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "39053344705", details["pix_key"])
}

// A CPF-typed Pix key IS the holder's tax id. Leaving tax_id off when the key
// already carries it would register a destination Ozav cannot match to a
// person, and the contract carries RecipientDocument only for the key types
// that are not a document.
func TestCreateOfframpDerivesTheTaxIdFromACpfKey(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)

	registered := fake.call(http.MethodPost, "/payout-destinations")
	require.NotNil(t, registered)
	var body map[string]any
	require.NoError(t, json.Unmarshal(registered.body, &body))
	holder := body["account_holder"].(map[string]any)
	taxID, ok := holder["tax_id"].(map[string]any)
	require.True(t, ok, "a cpf key must register the holder's tax id")
	require.Equal(t, "BR", taxID["country"])
	require.Equal(t, "cpf", taxID["type"])
	require.Equal(t, "39053344705", taxID["value"])
}

// An email key is not a document, so the CPF has to come from the contract's
// RecipientDocument instead.
func TestCreateOfframpTakesTheTaxIdFromRecipientDocumentForANonDocumentKey(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	req := offrampCreate()
	req.Destination.PixKey = "luiza@example.com"
	req.Destination.PixKeyType = "email"
	req.Destination.RecipientDocument = "39053344705"

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	registered := fake.call(http.MethodPost, "/payout-destinations")
	require.NotNil(t, registered)
	var body map[string]any
	require.NoError(t, json.Unmarshal(registered.body, &body))
	holder := body["account_holder"].(map[string]any)
	taxID := holder["tax_id"].(map[string]any)
	require.Equal(t, "39053344705", taxID["value"])
	require.Equal(t, "cpf", taxID["type"])
	require.Equal(t, "luiza@example.com", body["details"].(map[string]any)["pix_key"])
}

func TestCreateOfframpDoesNotPostPayoutTransfer(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)

	require.Nil(t, fake.call(http.MethodPost, "/transfers"), "payout transfer must not run on create")
}

func TestCreateOfframpReturnsAwaitingFundsWithDepositAddress(t *testing.T) {
	adapter, _ := newFakeAdapter(t, offrampRoutes())

	order, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)

	require.Equal(t, models.TxStatusAwaitingFunds, order.Status)
	require.Empty(t, order.ProviderOrderID)
	require.Equal(t, "OzavManagedDepositSol", order.DepositInstructions["to_address"])
	require.Equal(t, "solana", order.DepositInstructions["payment_rail"])
}

func TestCancelOfframpBeforeTheDepositCallsNothingUpstream(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	err := adapter.CancelOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, "")
	require.NoError(t, err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Empty(t, fake.requests, "an unfunded Ozav off-ramp has no transfer to cancel")
}

func TestCancelOfframpRefusesAnOrderAlreadySettled(t *testing.T) {
	adapter, _ := newFakeAdapter(t, offrampRoutes())

	err := adapter.CancelOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, "ozav_tr_1")
	require.Error(t, err)
}

func TestCreateOfframpDepositAddressWithoutCurrencyField(t *testing.T) {
	routes := offrampRoutes()
	// Measured live Ozav: network per row, no currency on deposit_address objects.
	routes["GET /wallets/ozav_wal_1/deposit-addresses"] = fakeReply{body: `{"data":[{"address":"4xydeSol","network":"solana","object":"deposit_address"}]}`}
	adapter, _ := newFakeAdapter(t, routes)

	req := offrampCreate()
	req.Source.Currency = "BRS"
	req.Source.PaymentRail = "solana"

	order, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)
	require.Equal(t, "4xydeSol", order.DepositInstructions["to_address"])
}

func TestSettleOfframpPostsTransferWithSourceChain(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	req := offrampCreate()
	req.Source.PaymentRail = "solana"
	_, err := adapter.SettleOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"},
		ramp.OfframpSettleRequest{
			OfframpCreateRequest: req,
			ExternalAccountID:    "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT",
		})
	require.NoError(t, err)

	created := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, created)
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))

	require.Equal(t, "ozav_q_1", body["quote_id"])
	source := body["source"].(map[string]any)
	require.Equal(t, "wallet", source["type"])
	require.Equal(t, "ozav_wal_1", source["wallet_id"])
	require.Equal(t, "USDC", source["currency"])
	require.Equal(t, "solana", source["chain"])

	destination := body["destination"].(map[string]any)
	require.Equal(t, "payout_destination", destination["type"])
	require.Equal(t, "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT", destination["payout_destination_id"])
	require.Equal(t, "offramp-0193f2ac8d5e7b21", created.header.Get("Idempotency-Key"))
}

func TestSettleOfframpSendsSourceChainForBrs(t *testing.T) {
	routes := offrampRoutes()
	routes["GET /wallets/ozav_wal_1/deposit-addresses"] = fakeReply{body: `{"data":[{"address":"OzavBrsDeposit","network":"solana","currency":"BRS"}]}`}
	adapter, fake := newFakeAdapter(t, routes)

	req := offrampCreate()
	req.Source.Currency = "brs"
	req.Source.PaymentRail = "solana"

	_, err := adapter.SettleOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"},
		ramp.OfframpSettleRequest{
			OfframpCreateRequest: req,
			ExternalAccountID:    "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT",
		})
	require.NoError(t, err)

	created := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, created)
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))
	source := body["source"].(map[string]any)
	require.Equal(t, "BRS", source["currency"])
	require.Equal(t, "solana", source["chain"])
}

// Registering the same Pix key twice would leave a second destination behind
// on every retry. The service persists ProviderDestinationRef precisely so
// the next order can name it.
func TestCreateOfframpReusesAnAlreadyRegisteredDestination(t *testing.T) {
	adapter, fake := newFakeAdapter(t, offrampRoutes())

	req := offrampCreate()
	req.ExternalAccountID = "ozav_pd_existing"

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	require.Nil(t, fake.call(http.MethodPost, "/payout-destinations"),
		"a known destination must not be registered a second time")
	require.Nil(t, fake.call(http.MethodPost, "/transfers"), "create must not post payout transfer")
}

func TestCreateOfframpReportsTheOrderAndTheDestinationItRegistered(t *testing.T) {
	adapter, _ := newFakeAdapter(t, offrampRoutes())

	order, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)

	require.Empty(t, order.ProviderOrderID)
	require.Equal(t, models.TxStatusAwaitingFunds, order.Status)
	require.Equal(t, "OzavManagedDepositSol", order.DepositInstructions["to_address"])
	require.Equal(t, "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT", order.ProviderDestinationRef,
		"persisting it is what stops the next order registering a duplicate")
}

// Ozav's own schema says to treat this as retryable rather than as a bad
// request: the destination is registered, the settlement rail just has not
// finished enabling it. Reporting it as a permanent refusal would tell the
// user to give up on an order that will work minutes later.
func TestSettleOfframpTreatsProvisioningRequiredAsRetryable(t *testing.T) {
	routes := offrampRoutes()
	routes["POST /transfers"] = fakeReply{status: http.StatusUnprocessableEntity, body: provisioningRequiredBody}
	adapter, _ := newFakeAdapter(t, routes)

	_, err := adapter.SettleOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"},
		ramp.OfframpSettleRequest{
			OfframpCreateRequest: offrampCreate(),
			ExternalAccountID:    "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT",
		})
	require.ErrorIs(t, err, ramp.ErrOzavPayoutNotProvisioned)
}

func TestCreateOfframpRequiresTheQuote(t *testing.T) {
	req := offrampCreate()
	req.ProviderQuoteID = ""

	_, err := unreachableAdapter(t).CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.ErrorIs(t, err, ramp.ErrEstimateRequired)
}

// The router reads Supports to pick who prices a rail, and the create that
// redeems that quote is sticky to the same provider. A rail declared while
// its create refuses is a quote the user can never redeem — this is the guard
// the package documented but never had.
func TestSupportsDeclaresOnlyRailsWhoseCreateIsImplemented(t *testing.T) {
	adapter := &Adapter{}

	for _, tc := range []struct {
		rail ramp.Rail
		want bool
	}{
		{ramp.RailPixPayout, true},
		{ramp.RailPixPayin, true},
	} {
		require.Equal(t, tc.want, adapter.Supports(ramp.Capability{Rail: tc.rail}), "Supports(%s)", tc.rail)
	}

	// A declared rail whose create is hard-disabled is the exact drift this
	// guards: the create must reach the provider, not refuse on a constant.
	offramps, _ := newFakeAdapter(t, offrampRoutes())
	_, err := offramps.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampCreate())
	require.NoError(t, err)
	require.NotErrorIs(t, err, ramp.ErrOfframpCreateUnavailable,
		"pix_payout is declared, so its create must not be hard-disabled")

	onramps, _ := newFakeAdapter(t, onrampRoutes())
	_, err = onramps.CreateOnramp(
		context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.NoError(t, err, "pix_payin is declared, so its create must complete too")
}

// registerDestination runs one off-ramp create for the key and answers the
// Idempotency-Key and body its payout-destination registration went out with.
func registerDestination(t *testing.T, pixKey, keyType, document string) (string, map[string]any) {
	t.Helper()
	return registerDestinationTo(t, offrampQuote().Destination.AccountOwnerName, pixKey, keyType, document)
}

// registerDestinationTo is registerDestination under another holder name.
func registerDestinationTo(t *testing.T, holder, pixKey, keyType, document string) (string, map[string]any) {
	t.Helper()
	adapter, fake := newFakeAdapter(t, offrampRoutes())
	req := offrampCreate()
	req.Destination.PixKey, req.Destination.PixKeyType = pixKey, keyType
	req.Destination.RecipientDocument = document
	req.Destination.AccountOwnerName = holder
	_, err := adapter.CreateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)
	registered := fake.call(http.MethodPost, "/payout-destinations")
	require.NotNil(t, registered)
	var body map[string]any
	require.NoError(t, json.Unmarshal(registered.body, &body))
	return registered.header.Get("Idempotency-Key"), body
}

// The destination's idempotency key is a digest of what makes the
// registration distinct: the customer, the key and the holder document. Built
// from the raw key, two keys that normalize alike collided on Ozav's alphabet
// (joao.silva@x.com and joao-silva@x.com both became joao-silva-x-com), the
// second registration replayed the first with another body, and every
// off-ramp to it failed on IDEMPOTENCY_KEY_REUSED. The header also carried the
// Pix key.
func TestRegisterPayoutDestinationKeysOnADigest(t *testing.T) {
	keyFor := func(t *testing.T, pixKey, keyType, document string) string {
		t.Helper()
		key, _ := registerDestination(t, pixKey, keyType, document)
		return key
	}

	dotted := keyFor(t, "joao.silva@x.com", "email", "39053344705")
	require.Regexp(t, `^[A-Za-z0-9_-]{16,256}$`, dotted, "sent as is: normalization must not touch it")
	require.NotContains(t, dotted, "joao", "a Pix key is personal data")
	require.Equal(t, dotted, keyFor(t, "joao.silva@x.com", "email", "39053344705"), "a retry reuses the key")
	require.NotEqual(t, dotted, keyFor(t, "joao-silva@x.com", "email", "39053344705"),
		"keys that normalize alike are still two destinations")
	require.NotEqual(t, dotted, keyFor(t, "joao.silva@x.com", "email", "11144477735"),
		"the same key under another document is another registration")
}

// A key goes out in its canonical DICT form, the one offramp_destinations
// hashes it by and the app already sends, so a retry that spells the same key
// another way is the same registration: same Idempotency-Key and same body,
// which Ozav replays. Measured in the sandbox on 23/09: a spaced phone is
// stored verbatim as a destination of its own, and the same key with another
// body is a 409 IDEMPOTENCY_KEY_REUSED, so the key alone would not do.
func TestRegisterPayoutDestinationSendsTheCanonicalKey(t *testing.T) {
	canonicalKey, _ := registerDestination(t, "+5511987654321", "phone", "39053344705")
	for _, spelling := range []string{"+55 11 98765-4321", "11987654321", "(11) 98765-4321"} {
		key, body := registerDestination(t, spelling, "phone", "39053344705")
		require.Equal(t, canonicalKey, key, spelling)
		require.Equal(t, "+5511987654321", body["details"].(map[string]any)["pix_key"], spelling)
	}

	digitsKey, _ := registerDestination(t, "39053344705", "cpf", "")
	key, body := registerDestination(t, "390.533.447-05", "cpf", "")
	require.Equal(t, digitsKey, key)
	require.Equal(t, "39053344705", body["details"].(map[string]any)["pix_key"])
	taxID := body["account_holder"].(map[string]any)["tax_id"].(map[string]any)
	require.Equal(t, "39053344705", taxID["value"], "a cpf key is the holder document, in the same form")
}

// The key covers every body field that tells one destination from another,
// the holder name as sent included. Under a key without it, a retry with a
// corrected name would send another body under the same key, and Ozav answers
// that with 409 IDEMPOTENCY_KEY_REUSED instead of registering it.
func TestRegisterPayoutDestinationKeysOnTheHolderName(t *testing.T) {
	first, _ := registerDestinationTo(t, "Luiza Souza", "39053344705", "cpf", "")
	again, _ := registerDestinationTo(t, "Luiza Souza", "39053344705", "cpf", "")
	corrected, body := registerDestinationTo(t, "Luiza Souza Lima", "39053344705", "cpf", "")

	require.Equal(t, first, again, "a retry under the same name reuses the key")
	require.NotEqual(t, first, corrected, "another name is another body, so another key")
	require.Equal(t, "Luiza Souza Lima", body["account_holder"].(map[string]any)["name"])
}

// An alphanumeric CNPJ is a CNPJ by its fourteen characters, letters included
// (spec §19.1): reduced to its digits it would register the destination under
// a document no company holds.
func TestRegisterPayoutDestinationKeepsTheLettersOfACNPJ(t *testing.T) {
	_, body := registerDestination(t, "maria@example.com", "email", "12abc34501de35")
	taxID := body["account_holder"].(map[string]any)["tax_id"].(map[string]any)
	require.Equal(t, "cnpj", taxID["type"])
	require.Equal(t, "12ABC34501DE35", taxID["value"])

	_, body = registerDestination(t, "12.abc.345/01de-35", "cnpj", "")
	require.Equal(t, "12ABC34501DE35", body["details"].(map[string]any)["pix_key"])
	require.Equal(t, "12ABC34501DE35", body["account_holder"].(map[string]any)["tax_id"].(map[string]any)["value"])
}
