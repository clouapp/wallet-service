package bridge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Decimal places by leg: BRL, and a stablecoin with USDC's six.
const (
	fiatScale   = 2
	cryptoScale = 6
)

// transferScale is the most decimal places Bridge takes in a transfer's
// amount, whatever the currency's own scale. Past it the transfer is refused
// with `invalid_parameters` ("max precision exceeded for this currency"),
// counted on the string, so "3.7530" is refused as well. Measured in the
// sandbox on 25/09/2026; the docs show "1.234567" as a valid amount.
const transferScale = 2

// pixAccountType is what Bridge calls a BRL payout destination.
const pixAccountType = "pix"

// quotableCurrencies, keyed by the uppercase token, is exactly what
// rateCurrency can map: /exchange_rates knows `usd` and `usdt` and refuses
// anything else. Bridge also moves `usdb` and `usdg`, but with no rate pair
// for either, admitting them would trade a local "no provider available" for a
// 400 from the rate call.
var quotableCurrencies = map[string]bool{
	models.TargetTokenUSDC: true,
	models.TargetTokenUSDT: true,
}

// movableChains is where XIP has actually moved money through Bridge. Bridge
// settles on more, but the router picks this provider for any chain listed
// here, and a chain never exercised is a promise nothing can keep.
var movableChains = map[string]bool{
	models.ChainSolana: true,
}

// refuseCurrency declines a crypto leg Bridge cannot price with "no provider
// available" rather than a validation error: the token is a legitimate ask,
// just not one this provider serves.
func refuseCurrency(currency string) error {
	if quotableCurrencies[strings.ToUpper(currency)] {
		return nil
	}
	return fmt.Errorf("bridge: %s is not a currency it prices or moves: %w",
		normalizeCurrency(currency), ramp.ErrNoProviderAvailable)
}

// normalizeCurrency lowercases a currency code: Bridge refuses anything else
// ("USDC is not a valid value"). Addresses and payment rails are never
// normalized; a Solana address is case-sensitive base58.
func normalizeCurrency(currency string) string { return strings.ToLower(currency) }

// rateCurrency maps a token onto the currency Bridge quotes: asking
// /exchange_rates for `usdc` is a 400, and USDC is redeemable 1:1 for USD.
// Anything else passes through to fail loudly rather than be priced off the
// wrong pair.
func rateCurrency(currency string) string {
	if strings.EqualFold(currency, "usdc") {
		return "usd"
	}
	return strings.ToLower(currency)
}

// EstimateOnramp prices BRL in against the crypto the user would receive. The
// quote is indicative: Bridge holds no price for a transfer, so there is no
// quote id to redeem.
func (a *Adapter) EstimateOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampQuoteRequest) (*ramp.Estimate, error) {
	// The crypto leg is the destination here; gating the source let a SOL
	// quote reach /exchange_rates and come back as a 502.
	if err := refuseCurrency(req.Destination.Currency); err != nil {
		return nil, err
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	quote, err := a.exchangeRate(ctx, req.Source.Currency, req.Destination.Currency)
	if err != nil {
		return nil, err
	}
	// The fee comes out of the BRL the user sends.
	return estimate(estimateInput{
		amount:      req.Source.Amount,
		feePercent:  req.FeePercent,
		feeScale:    fiatScale,
		feeCurrency: normalizeCurrency(req.Source.Currency),
		quote:       quote,
		outScale:    cryptoScale,
		fiatIn:      true,
	})
}

// EstimateOfframp prices crypto in against the BRL the user would receive.
//
// It prices the part of the amount a transfer can carry (see transferScale)
// and names it in SourceAmount, so the create that follows sends exactly that.
func (a *Adapter) EstimateOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpQuoteRequest) (*ramp.Estimate, error) {
	if err := refuseCurrency(req.Source.Currency); err != nil {
		return nil, err
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	amount, err := ramp.FloorAmount(req.Source.Amount, transferScale)
	if err != nil {
		return nil, fmt.Errorf("bridge: amount: %w", err)
	}
	quote, err := a.exchangeRate(ctx, req.Source.Currency, req.Destination.Currency)
	if err != nil {
		return nil, err
	}
	// The fee comes out of the crypto leg, so it is quoted in the token.
	quoted, err := estimate(estimateInput{
		amount:      amount,
		feePercent:  req.FeePercent,
		feeScale:    cryptoScale,
		feeCurrency: normalizeCurrency(req.Source.Currency),
		quote:       quote,
		outScale:    fiatScale,
	})
	if err != nil {
		return nil, err
	}
	quoted.SourceAmount = amount
	return quoted, nil
}

// CreateOnramp refuses: Bridge collects BRL through a standing virtual account
// with no per-order instruction, served by the virtual-account service.
//
// ErrRequestNotSent is a statement of fact here, and the caller needs it to
// release its reservation; without it the retry dedups onto a pending row and
// the client gets an order with no deposit instructions.
func (a *Adapter) CreateOnramp(context.Context, ramp.CustomerRef, ramp.OnrampCreateRequest) (*ramp.RampOrder, error) {
	return nil, fmt.Errorf(
		"bridge: this on-ramp is collected through a virtual account, not a transfer: %w, %w",
		ramp.ErrNoProviderAvailable, ramp.ErrRequestNotSent)
}

// CreateOfframp places a crypto → BRL transfer, registering the PIX key first
// when the caller has no account id for it yet.
func (a *Adapter) CreateOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpCreateRequest) (*ramp.RampOrder, error) {
	if err := refuseCurrency(req.Source.Currency); err != nil {
		return nil, err
	}
	if err := requireCreatable(ref, req.IdempotencyKey); err != nil {
		return nil, err
	}

	accountID := req.ExternalAccountID
	if accountID == "" {
		resolved, err := a.ensureExternalAccount(ctx, ref, req.Destination)
		if err != nil {
			return nil, ramp.NotSent(err)
		}
		accountID = resolved
	}

	sourceCurrency := normalizeCurrency(req.Source.Currency)
	resp, raw, err := ramp.Call[transferResponse](ctx, a.do, Request{
		Method: http.MethodPost,
		Path:   "/transfers",
		Body: transferRequest{
			Amount:              req.Source.Amount,
			OnBehalfOf:          ref.ExternalID,
			DeveloperFeePercent: req.FeePercent,
			Source: transferLeg{
				PaymentRail: req.Source.PaymentRail,
				Currency:    sourceCurrency,
				FromAddress: req.Source.Address,
			},
			Destination: transferLeg{
				PaymentRail:       req.Destination.Rail,
				Currency:          normalizeCurrency(req.Destination.Currency),
				ExternalAccountID: accountID,
			},
		},
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	order := orderFromTransfer(resp, raw, req.FeePercent, sourceCurrency)
	order.ProviderDestinationRef = accountID
	return order, nil
}

// GetOrder re-reads a transfer. The fee percent and currency come back empty:
// the caller keeps what it persisted at create.
func (a *Adapter) GetOrder(ctx context.Context, ref ramp.CustomerRef, providerOrderID string) (*ramp.RampOrder, error) {
	if providerOrderID == "" {
		return nil, errors.New("bridge: get order requires a provider order id")
	}
	resp, raw, err := ramp.Call[transferResponse](ctx, a.do, Request{Method: http.MethodGet, Path: "/transfers/" + providerOrderID})
	if err != nil {
		return nil, err
	}
	return orderFromTransfer(resp, raw, "", ""), nil
}

// CancelOfframp withdraws a transfer that still awaits its deposit. Bridge
// refuses the DELETE in any other state, and its answer carries nothing the
// order needs: the canceled state also arrives as a transfer.updated event.
func (a *Adapter) CancelOfframp(ctx context.Context, _ ramp.CustomerRef, providerOrderID string) error {
	if providerOrderID == "" {
		return errors.New("bridge: cancel offramp requires a provider order id")
	}
	return a.do(ctx, Request{Method: http.MethodDelete, Path: "/transfers/" + providerOrderID}, nil)
}

var _ ramp.OfframpCanceler = (*Adapter)(nil)

// estimateInput is what both directions reduce to once the fee side and the
// output side are decided.
type estimateInput struct {
	amount      string
	feePercent  string
	feeScale    int
	feeCurrency string
	quote       *rateQuote
	outScale    int
	// fiatIn says the amount is the reais leg (an on-ramp).
	fiatIn bool
}

// rateQuote is a rate with the direction Bridge published it in: it quotes a
// pair one way only (usd→brl, never brl→usd), so the other direction divides.
// Rate stays exactly what Bridge said; it is the number the user is shown.
type rateQuote struct {
	Rate string
	// Market is the mid-market rate in the same direction; it differs from
	// Rate by Bridge's own conversion cut (see exchangeRateResponse).
	Market string
	// Multiply is true when the quote is destination-per-source.
	Multiply bool
	Raw      json.RawMessage
}

// estimate takes the rail fee off what the user sends, then converts the rest.
func estimate(in estimateInput) (*ramp.Estimate, error) {
	feeAmount, err := ramp.FeeAmount(in.amount, in.feePercent, in.feeScale)
	if err != nil {
		return nil, fmt.Errorf("bridge: fee: %w", err)
	}
	net, err := ramp.SubtractAmount(in.amount, feeAmount, in.feeScale)
	if err != nil {
		return nil, fmt.Errorf("bridge: net amount: %w", err)
	}
	convert := ramp.ConvertAmount
	if !in.quote.Multiply {
		convert = ramp.DivideAmount
	}
	receive, err := convert(net, in.quote.Rate, in.outScale)
	if err != nil {
		return nil, fmt.Errorf("bridge: convert: %w", err)
	}
	gapPercent, gapAmount, err := ramp.SpreadCost(net, in.quote.Rate, in.quote.Market, in.quote.Multiply, in.fiatIn)
	if err != nil {
		return nil, fmt.Errorf("bridge: conversion gap: %w", err)
	}
	return &ramp.Estimate{
		Binding:            false,
		Rate:               in.quote.Rate,
		FeePercent:         in.feePercent,
		FeeAmount:          feeAmount,
		FeeCurrency:        in.feeCurrency,
		EstimatedReceive:   receive,
		ProviderFeePercent: gapPercent,
		ProviderFeeAmount:  gapAmount,
		Raw:                in.quote.Raw,
	}, nil
}

// exchangeRate fetches the rate for a pair in whichever direction Bridge
// publishes it. The inverse is divided by rather than inverted here, which
// would round once before the amount arithmetic rounds again.
func (a *Adapter) exchangeRate(ctx context.Context, from, to string) (*rateQuote, error) {
	source, destination := rateCurrency(from), rateCurrency(to)

	rates, raw, err := a.fetchRate(ctx, source, destination)
	if err == nil {
		return &rateQuote{Rate: rates.applied(false), Market: rates.market(), Multiply: true, Raw: raw}, nil
	}
	inverse, inverseRaw, inverseErr := a.fetchRate(ctx, destination, source)
	if inverseErr != nil {
		return nil, err // it names the pair the caller asked for
	}
	return &rateQuote{Rate: inverse.applied(true), Market: inverse.market(), Multiply: false, Raw: inverseRaw}, nil
}

func (a *Adapter) fetchRate(ctx context.Context, from, to string) (exchangeRateResponse, json.RawMessage, error) {
	resp, raw, err := ramp.Call[exchangeRateResponse](ctx, a.do, Request{
		Method: http.MethodGet,
		Path:   "/exchange_rates",
		Query:  url.Values{"from": {from}, "to": {to}},
	})
	if err != nil {
		return exchangeRateResponse{}, nil, err
	}
	if resp.market() == "" {
		return exchangeRateResponse{}, nil, fmt.Errorf("bridge: no exchange rate for %s/%s", from, to)
	}
	return resp, raw, nil
}

// orderFromTransfer normalizes a created or re-read transfer. Amounts come
// from Bridge's receipt, which is what settles, never from local arithmetic.
func orderFromTransfer(resp transferResponse, raw json.RawMessage, feePercent, feeCurrency string) *ramp.RampOrder {
	amounts := map[string]string{
		"source_amount":      cmp.Or(resp.Receipt.InitialAmount, resp.Amount),
		"destination_amount": resp.Receipt.FinalAmount,
		"subtotal_amount":    resp.Receipt.SubtotalAmount,
		"exchange_rate":      resp.Receipt.ExchangeRate,
		"exchange_fee":       resp.Receipt.ExchangeFee,
	}
	maps.DeleteFunc(amounts, func(_, value string) bool { return value == "" })

	return &ramp.RampOrder{
		ProviderOrderID:     resp.ID,
		Status:              transferStatusFromBridge(resp.State),
		DepositInstructions: resp.SourceDepositInstructions,
		Amounts:             amounts,
		FeePercent:          feePercent,
		FeeAmount:           resp.Receipt.DeveloperFee,
		FeeCurrency:         feeCurrency,
		TxHash:              resp.Receipt.DestinationTxHash,
		ExpiresAt:           depositExpiry(resp.SourceDepositInstructions),
		Raw:                 raw,
	}
}

// ensureExternalAccount registers the customer's PIX payout destination.
//
// There is no read-then-create: Bridge returns the stored key masked
// (`account_preview` is "jo****ilva****il.c**"), so a listing can never match
// the key we hold. And Bridge refuses the same key twice (measured 16/09: 400
// `duplicate_external_account` in sandbox, 422 in production); the derived
// idempotency key hides that only while Bridge still caches the first call. The
// caller prevents the second registration: it records the id this returns in
// `offramp_destinations` and names it on the next order to the key.
func (a *Adapter) ensureExternalAccount(ctx context.Context, ref ramp.CustomerRef, dest ramp.PixDestination) (string, error) {
	if dest.PixKey == "" {
		return "", errors.New("bridge: offramp destination has no pix key")
	}
	// Bridge takes the CPF/CNPJ apart from the key; a document key is both.
	doc := dest.RecipientDocument
	if dest.PixKeyType == "cpf" || dest.PixKeyType == "cnpj" {
		doc = dest.PixKey
	}
	if doc == "" {
		return "", errors.New("bridge: offramp destination requires recipient_document for this pix key type")
	}

	var created externalAccountResponse
	err := a.do(ctx, Request{
		Method: http.MethodPost,
		Path:   customerPath(ref.ExternalID) + "/external_accounts",
		Body:   pixExternalAccount(dest, doc),
		// The owner type is not part of it: it follows the key type, which
		// already is (spec §19.3).
		IdempotencyKey: "ext-acct:" + ref.ExternalID + ":" + dest.PixKey + ":" + doc,
	}, &created)
	if err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("bridge: external account created without an id")
	}
	return created.ID, nil
}

// pixExternalAccount is the payout destination for a Pix key. A CNPJ key
// belongs to a company, so its owner is a business, named as business_name
// and, because Bridge refuses a business account without it (sandbox,
// 18/10/2026), as account_owner_name too; every other key is an individual's.
func pixExternalAccount(dest ramp.PixDestination, document string) externalAccountRequest {
	body := externalAccountRequest{
		Currency:         normalizeCurrency(dest.Currency),
		AccountType:      pixAccountType,
		AccountOwnerType: customerTypeIndividual,
		AccountOwnerName: dest.AccountOwnerName,
		PixKey:           &pixKeyPayload{PixKey: dest.PixKey, DocumentNumber: document},
	}
	if dest.PixKeyType == "cnpj" {
		body.AccountOwnerType = customerTypeBusiness
		body.BusinessName = dest.AccountOwnerName
	}
	return body
}

// requireCreatable refuses a create with no idempotency key: without one,
// every retry of a timed-out create would place a second transfer.
func requireCreatable(ref ramp.CustomerRef, idempotencyKey string) error {
	if ref.ExternalID == "" {
		return ramp.ErrIntegrationRequired
	}
	if idempotencyKey == "" {
		return errors.New("bridge: create requires an idempotency key")
	}
	return nil
}
