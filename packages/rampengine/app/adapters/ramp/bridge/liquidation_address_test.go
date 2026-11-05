package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// achAddressBody is a liquidation address as Bridge answers it.
const achAddressBody = `{
	"id":"la_1",
	"customer_id":"cus_1",
	"chain":"solana",
	"currency":"usdc",
	"address":"BridgeLiquidationAddr1",
	"external_account_id":"ext_1",
	"destination_payment_rail":"ach",
	"destination_currency":"usd",
	"destination_ach_reference":"XIP 001",
	"custom_developer_fee_percent":"1.0",
	"state":"active"
}`

// wireAddressBody pays the same account over a wire.
const wireAddressBody = `{
	"id":"la_2",
	"customer_id":"cus_1",
	"chain":"solana",
	"currency":"usdc",
	"address":"BridgeLiquidationAddr2",
	"external_account_id":"ext_1",
	"destination_payment_rail":"wire",
	"destination_currency":"usd",
	"destination_wire_message":"Rent October",
	"state":"active"
}`

func achAddressRequest() ramp.LiquidationAddressRequest {
	return ramp.LiquidationAddressRequest{
		Rail: ramp.RailUSDACHPayout,
		// Uppercase as the client API writes a token; Bridge only takes lowercase.
		Source:            ramp.CryptoLeg{Currency: "USDC", PaymentRail: "solana"},
		ExternalAccountID: "ext_1",
		ReturnAddress:     solanaAddress,
		Reference:         "XIP 001",
		FeePercent:        "1.0",
		IdempotencyKey:    "la:7:12:usd_ach_payout:solana:usdc",
	}
}

var bridgeCustomer = ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}

// Bridge does not promise to deduplicate liquidation addresses, so, as with a
// virtual account, the listing comes first and a create only follows a miss.
func TestProvisionLiquidationAddressListsBeforeCreating(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses":  listBody(),
		"POST /customers/cus_1/liquidation_addresses": achAddressBody,
	})

	address, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, achAddressRequest())
	require.NoError(t, err)

	require.Len(t, *calls, 2, "the listing has to happen before the create")
	require.Equal(t, http.MethodGet, (*calls)[0].method)
	require.Equal(t, "limit=100", (*calls)[0].query)
	require.Equal(t, http.MethodPost, (*calls)[1].method)
	require.Equal(t, map[string]any{
		"chain":                        "solana",
		"currency":                     "usdc",
		"external_account_id":          "ext_1",
		"destination_payment_rail":     "ach",
		"destination_currency":         "usd",
		"destination_ach_reference":    "XIP 001",
		"return_instructions":          map[string]any{"address": solanaAddress},
		"custom_developer_fee_percent": "1.0",
	}, (*calls)[1].body)
	require.Equal(t, "la:7:12:usd_ach_payout:solana:usdc", (*calls)[1].header.Get("Idempotency-Key"))

	require.Equal(t, "la_1", address.ProviderAddressID)
	require.Equal(t, "BridgeLiquidationAddr1", address.Address)
	require.Equal(t, models.LiquidationAddressStatusActive, address.Status)
	require.Equal(t, ramp.RailUSDACHPayout, address.Rail)
	require.Equal(t, "solana", address.Chain)
	require.Equal(t, "usdc", address.Currency)
	require.Equal(t, "ext_1", address.ExternalAccountID)
	require.Equal(t, "XIP 001", address.Reference)
	require.Equal(t, "1.0", address.FeePercent)
	require.NotEmpty(t, address.Raw)
}

// A wire carries its reference as destination_wire_message; with no caller's
// key the key is derived from the route, and an empty fee is not sent.
func TestProvisionLiquidationAddressSendsAWireMessageOnWire(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses":  listBody(),
		"POST /customers/cus_1/liquidation_addresses": wireAddressBody,
	})

	req := achAddressRequest()
	req.Rail = ramp.RailUSDWirePayout
	req.Reference = "Rent October"
	req.FeePercent = ""
	req.IdempotencyKey = ""

	address, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, req)
	require.NoError(t, err)

	created := (*calls)[1]
	require.Equal(t, "wire", created.body["destination_payment_rail"])
	require.Equal(t, "Rent October", created.body["destination_wire_message"])
	require.NotContains(t, created.body, "destination_ach_reference")
	require.NotContains(t, created.body, "custom_developer_fee_percent")
	require.Equal(t, "la:cus_1:ext_1:wire:solana:usdc", created.header.Get("Idempotency-Key"))

	require.Equal(t, ramp.RailUSDWirePayout, address.Rail)
	require.Equal(t, "Rent October", address.Reference)
}

// An address already paying the same account, over the same rail, from the
// same token and chain is returned as-is. A deactivated one, or one for
// another rail, is not the route asked for.
func TestProvisionLiquidationAddressReturnsTheExistingRoute(t *testing.T) {
	deactivated := strings.NewReplacer(`"la_1"`, `"la_0"`, `"active"`, `"deactivated"`).Replace(achAddressBody)
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses": listBody(deactivated, wireAddressBody, achAddressBody),
	})

	address, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, achAddressRequest())
	require.NoError(t, err)
	require.Len(t, *calls, 1, "no create when the route exists")
	require.Equal(t, "la_1", address.ProviderAddressID)
}

// Bridge keeps one address per (external account, chain, currency, rail):
// the sandbox (2026-10-15) answered a second create for the same route with
// 400 invalid_parameters "Liquidation address with this external account on
// this chain, currency and destination_payment_rail already exists". That is a
// create racing another (or an answer lost); the address exists, so it is
// read again and returned instead of failing as a 502.
func TestProvisionLiquidationAddressRereadsWhenTheRouteAlreadyExists(t *testing.T) {
	var methods []string
	listed := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"Liquidation address with this external account on this chain, currency and destination_payment_rail already exists for this customer"}`))
			return
		}
		listed++
		if listed == 1 {
			_, _ = w.Write([]byte(listBody()))
			return
		}
		_, _ = w.Write([]byte(listBody(achAddressBody)))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)

	address, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, achAddressRequest())
	require.NoError(t, err)
	require.Equal(t, []string{http.MethodGet, http.MethodPost, http.MethodGet}, methods)
	require.Equal(t, "la_1", address.ProviderAddressID)
}

// An invalid_parameters that is not the route existing (nothing found on the
// second read) stays the upstream error.
func TestProvisionLiquidationAddressKeepsARejectionWhenNothingExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"bad"}`))
			return
		}
		_, _ = w.Write([]byte(listBody()))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)

	_, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, achAddressRequest())
	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrRequestNotSent)
}

// The listing failed, so the create was never sent: the service may release
// its reservation and let the user try again.
func TestProvisionLiquidationAddressMarksAFailedListingAsNotSent(t *testing.T) {
	adapter, _ := statusAdapter(t, http.StatusInternalServerError, `{"code":"server_error","message":"boom"}`)

	_, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, achAddressRequest())
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
}

// A rail with no bank network behind it, or a token Bridge does not move, is
// "no provider", and nothing is sent.
func TestProvisionLiquidationAddressRefusesWhatItCannotPayOut(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})

	req := achAddressRequest()
	req.Rail = ramp.RailPixPayout
	_, err := adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, req)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)

	req = achAddressRequest()
	req.Source.Currency = "BTC"
	_, err = adapter.ProvisionLiquidationAddress(context.Background(), bridgeCustomer, req)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)

	_, err = adapter.ProvisionLiquidationAddress(context.Background(), ramp.CustomerRef{CustomerID: 7}, achAddressRequest())
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	require.Empty(t, *calls)
}

// The reference lives in the field of the address's own rail; the PUT carries
// only what changes and no idempotency key: the sandbox (2026-10-15) answers a
// PUT that carries one with 422 "Unexpected Idempotency Key".
func TestUpdateLiquidationAddressChangesTheReferenceOfItsRail(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_2": wireAddressBody,
		"PUT /customers/cus_1/liquidation_addresses/la_2": strings.Replace(wireAddressBody, "Rent October", "Rent November", 1),
	})

	reference := "Rent November"
	address, err := adapter.UpdateLiquidationAddress(context.Background(), bridgeCustomer, "la_2",
		ramp.LiquidationAddressUpdate{Reference: &reference})
	require.NoError(t, err)

	require.Len(t, *calls, 2)
	require.Equal(t, http.MethodPut, (*calls)[1].method)
	require.Equal(t, map[string]any{"destination_wire_message": "Rent November"}, (*calls)[1].body)
	require.Empty(t, (*calls)[1].header.Get("Idempotency-Key"))
	require.Equal(t, "Rent November", address.Reference)
}

// An empty reference clears it: Bridge reads null as "remove" (sandbox
// 2026-10-15: null → 200; "" → 400 "must be filled").
func TestUpdateLiquidationAddressClearsTheReferenceWithNull(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1": achAddressBody,
		"PUT /customers/cus_1/liquidation_addresses/la_1": strings.Replace(achAddressBody, `"XIP 001"`, `null`, 1),
	})

	cleared := ""
	address, err := adapter.UpdateLiquidationAddress(context.Background(), bridgeCustomer, "la_1",
		ramp.LiquidationAddressUpdate{Reference: &cleared, FeePercent: "2.0"})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"destination_ach_reference":    nil,
		"custom_developer_fee_percent": "2.0",
	}, (*calls)[1].body)
	require.Empty(t, address.Reference)
}

// A new return wallet goes in return_instructions, and the answer's is read
// back.
func TestUpdateLiquidationAddressMovesTheReturnAddress(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1": achAddressBody,
		"PUT /customers/cus_1/liquidation_addresses/la_1": strings.Replace(achAddressBody, `"state"`,
			`"return_instructions":{"address":"NewReturnWallet111"},"state"`, 1),
	})

	address, err := adapter.UpdateLiquidationAddress(context.Background(), bridgeCustomer, "la_1",
		ramp.LiquidationAddressUpdate{ReturnAddress: "NewReturnWallet111"})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"return_instructions": map[string]any{"address": "NewReturnWallet111"},
	}, (*calls)[1].body)
	require.Equal(t, "NewReturnWallet111", address.ReturnAddress)
}

func TestUpdateLiquidationAddressWithNothingToChangeOnlyReads(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1": achAddressBody,
	})

	address, err := adapter.UpdateLiquidationAddress(context.Background(), bridgeCustomer, "la_1",
		ramp.LiquidationAddressUpdate{})
	require.NoError(t, err)
	require.Len(t, *calls, 1)
	require.Equal(t, "la_1", address.ProviderAddressID)
}
