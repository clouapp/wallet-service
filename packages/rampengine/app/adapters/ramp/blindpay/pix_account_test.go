package blindpay_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	pixAccountID   = "ba_000000000009"
	pixAccountName = "xip-pix-0123456789abcdef0123"
	pixKeyValue    = "ana.souza@example.com"
	pixDocument    = "39053344705"
	pixOwnerName   = "Ana Souza"
)

func pixRegistration() ramp.PixAccountRegistration {
	return ramp.PixAccountRegistration{
		Name: pixAccountName, PixKeyType: "email", PixKey: pixKeyValue,
		RecipientName: pixOwnerName, RecipientDocument: pixDocument, Relationship: ramp.RelationshipOther,
	}
}

func pixAccountBody(overrides map[string]any) string {
	body := map[string]any{"id": pixAccountID, "type": "pix", "name": pixAccountName, "status": "approved"}
	for k, v := range overrides {
		body[k] = v
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func TestRegisterPixAccount_SendsTheFixedBodyAndReadsTheEcho(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST customers/" + customerID + "/bank-accounts": {200, pixAccountBody(map[string]any{"status": "verifying"})},
	})

	res, err := adapter.RegisterPixAccount(context.Background(), payinRef, pixRegistration(), "pix-reg:x:1")

	require.NoError(t, err)
	assert.Equal(t, pixAccountID, res.ExternalAccountID)
	assert.Equal(t, ramp.BankAccountVerifying, res.Status)
	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "pix-reg:x:1", got[0].idempotency)
	assert.Equal(t,
		`{"type":"pix","name":"`+pixAccountName+`","recipient_relationship":"other","pix_key":"`+pixKeyValue+`","beneficiary_name":"`+pixOwnerName+`","tax_id":"`+pixDocument+`"}`,
		string(got[0].body))
}

func TestRegisterPixAccount_TheSameRegistrationEncodesToTheSameBytes(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST customers/" + customerID + "/bank-accounts": {200, pixAccountBody(nil)},
	})
	for range 2 {
		_, err := adapter.RegisterPixAccount(context.Background(), payinRef, pixRegistration(), "pix-reg:x:1")
		require.NoError(t, err)
	}
	got := calls()
	require.Len(t, got, 2)
	assert.Equal(t, got[0].body, got[1].body)
}

func TestRegisterPixAccount_RefusesAnEchoThatNamesAnotherAccount(t *testing.T) {
	cases := map[string]string{
		"another name":    pixAccountBody(map[string]any{"name": "xip-pix-other"}),
		"another type":    pixAccountBody(map[string]any{"type": "ach"}),
		"no ba_ prefix":   pixAccountBody(map[string]any{"id": "re_000000000009"}),
		"undecodable 200": `[1,2`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
				"POST customers/" + customerID + "/bank-accounts": {200, body},
			})
			_, err := adapter.RegisterPixAccount(context.Background(), payinRef, pixRegistration(), "k")
			assert.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
		})
	}
}

func TestRegisterPixAccount_SendsNothingWithoutACustomerOrAKey(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)

	_, err := adapter.RegisterPixAccount(context.Background(), ramp.CustomerRef{}, pixRegistration(), "k")
	assert.ErrorIs(t, err, ramp.ErrIntegrationRequired)

	reg := pixRegistration()
	reg.PixKey = ""
	_, err = adapter.RegisterPixAccount(context.Background(), payinRef, reg, "k")
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)
	assert.Empty(t, calls())
}

func TestRegisterPixAccount_NeverWrapsTheKeyTheDocumentOrTheName(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST customers/" + customerID + "/bank-accounts": {400, `{"code":"INVALID_PIX_KEY","message":"` + pixKeyValue + ` ` + pixDocument + ` ` + pixOwnerName + `"}`},
	})

	_, err := adapter.RegisterPixAccount(context.Background(), payinRef, pixRegistration(), "k")

	require.Error(t, err)
	for _, secret := range []string{pixKeyValue, pixDocument, pixOwnerName} {
		assert.NotContains(t, err.Error(), secret)
	}
}

func TestFindPixAccountByName_SkipsADeprecatedAccountAndRefusesAmbiguity(t *testing.T) {
	list := func(items ...string) string {
		out := "["
		for i, item := range items {
			if i > 0 {
				out += ","
			}
			out += item
		}
		return out + "]"
	}
	live := pixAccountBody(nil)
	gone := pixAccountBody(map[string]any{"id": "ba_000000000010", "status": "deprecated"})
	twin := pixAccountBody(map[string]any{"id": "ba_000000000011"})
	other := pixAccountBody(map[string]any{"id": "ba_000000000012", "name": "xip-pix-other"})
	route := "GET customers/" + customerID + "/bank-accounts"

	t.Run("one live account", func(t *testing.T) {
		adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{route: {200, list(gone, live, other)}})
		res, err := adapter.FindPixAccountByName(context.Background(), payinRef, pixAccountName)
		require.NoError(t, err)
		assert.Equal(t, pixAccountID, res.ExternalAccountID)
		assert.Contains(t, calls()[0].path, "bank-accounts")
	})
	t.Run("none", func(t *testing.T) {
		adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{route: {200, list(gone, other)}})
		res, err := adapter.FindPixAccountByName(context.Background(), payinRef, pixAccountName)
		require.NoError(t, err)
		assert.Nil(t, res)
	})
	t.Run("ambiguous", func(t *testing.T) {
		adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{route: {200, list(live, twin)}})
		_, err := adapter.FindPixAccountByName(context.Background(), payinRef, pixAccountName)
		assert.ErrorIs(t, err, ramp.ErrBankAccountNameAmbiguous)
	})
}

func TestGetPixAccount_ReadsTheStatusAndRefusesAnotherType(t *testing.T) {
	route := "GET customers/" + customerID + "/bank-accounts/" + pixAccountID
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{route: {200, pixAccountBody(nil)}})
	res, err := adapter.GetPixAccount(context.Background(), payinRef, pixAccountID)
	require.NoError(t, err)
	assert.Equal(t, ramp.BankAccountApproved, res.Status)

	adapter, _, _ = payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{route: {200, pixAccountBody(map[string]any{"type": "ach"})}})
	_, err = adapter.GetPixAccount(context.Background(), payinRef, pixAccountID)
	assert.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)

	_, err = adapter.GetPixAccount(context.Background(), payinRef, "x")
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)
}

func pixOfframpRequest(amount, fee string) ramp.OfframpQuoteRequest {
	return ramp.OfframpQuoteRequest{
		Source:      ramp.CryptoLeg{Currency: "USDC", PaymentRail: "solana", Amount: amount},
		Destination: ramp.PixDestination{PixKeyType: "email", ExternalAccountID: pixAccountID},
		FeePercent:  fee,
	}
}

func pixQuoteBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	merged := map[string]any{"receiver_amount": 5420, "partner_fee_amount": 10, "flat_fee": 20}
	for k, v := range overrides {
		merged[k] = v
	}
	return bankQuoteBody(t, merged)
}

func TestEstimateOfframp_PixQuotesInRealsWithThePartnerFee(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET partner-fees": {200, "[" + feeProfile(partnerFee, 0, 100) + "]"},
		"POST quotes":      {200, pixQuoteBody(t, nil)},
	})

	est, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("10.123456", "1.00"))

	require.NoError(t, err)
	got := calls()
	require.Len(t, got, 2)
	assert.Equal(t,
		`{"bank_account_id":"`+pixAccountID+`","currency_type":"sender","cover_fees":false,"request_amount":1012,"network":"solana_devnet","token":"USDB","partner_fee_id":"`+partnerFee+`"}`,
		string(got[1].body))
	assert.Equal(t, quoteID, est.ProviderQuoteID)
	assert.True(t, est.Binding)
	assert.Equal(t, "54.20", est.EstimatedReceive, "receiver_amount is in reais")
	assert.Equal(t, "10.12", est.SourceAmount)
	assert.Equal(t, "1.00", est.FeePercent)
	assert.Equal(t, "0.10", est.FeeAmount)
	assert.Equal(t, "0.20", est.ProviderFeeAmount)
}

func TestEstimateOfframp_PixWithAZeroFeeSendsNoPartnerFee(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, pixQuoteBody(t, map[string]any{"partner_fee_amount": nil})},
	})
	est, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("10.12", "0"))
	require.NoError(t, err)
	assert.NotContains(t, string(calls()[0].body), "partner_fee_id")
	assert.Equal(t, "0.00", est.FeeAmount)
}

func TestEstimateOfframp_PixWithoutAnAccountIsNotSent(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
	req := pixOfframpRequest("10.12", "1.00")
	req.Destination.ExternalAccountID = ""
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, req)
	assert.ErrorIs(t, err, ramp.ErrInvalidDestination)
	assert.Empty(t, calls())
}

func TestEstimateOfframp_PixBelowTheSandboxMinimumNamesLimitUSD(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("9.99", "1.00"))
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "10.00", violation.LimitUSD, "the sandbox refuses a Pix quote under US$ 10 with a 502")
	assert.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	assert.Empty(t, calls())
}

func TestEstimateOfframp_PixProductionKeepsTheFiveDollarFloor(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeProduction, nil)
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("4.99", "1.00"))
	var violation *ramp.LimitViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "5.00", violation.LimitUSD)
	assert.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	assert.Empty(t, calls())

	adapter, _, calls = payinServer(t, models.ProviderModeProduction, map[string]stubAnswer{
		"POST quotes": {200, pixQuoteBody(t, map[string]any{"partner_fee_amount": nil, "sender_amount": 700})},
	})
	_, err = adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("7.00", "0"))
	require.NoError(t, err, "production is not held to the sandbox's US$ 10")
	assert.Len(t, calls(), 1)
}

func TestEstimateOfframp_PixForgetsAPartnerFeeTheProviderRefused(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET partner-fees": {200, "[" + feeProfile(partnerFee, 0, 100) + "]"},
		"POST quotes":      {404, `{"code":"FEES_NOT_FOUND"}`},
	})
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("10.12", "1.00"))
	require.Error(t, err)
	// The next call reads the list again: the cached id was dropped.
	_, _ = adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("10.12", "1.00"))
	lists := 0
	for _, c := range calls() {
		if c.method == "GET" && c.path == instancePrefix+"partner-fees" {
			lists++
		}
	}
	assert.Equal(t, 2, lists)
}

func TestEstimateOfframp_PixRefusesAMalformedQuote(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, pixQuoteBody(t, map[string]any{"partner_fee_amount": -1})},
	})
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, pixOfframpRequest("10.12", "0"))
	assert.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestSupports_PixPayoutOnlyWhereBankPayoutsAreOpen(t *testing.T) {
	open := func(token string) bool { return token == "USDC" }
	assert.True(t, blindpay.New(nil, nil, open).Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"}))
	assert.False(t, blindpay.New(nil, nil, open).Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"}))
	assert.False(t, blindpay.New(nil, nil, open).Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "ethereum"}))
	assert.False(t, blindpay.New(nil, nil, open).Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "BRZ", Chain: "solana"}))
	assert.False(t, blindpay.New(nil, nil, nil).Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"}))
}
