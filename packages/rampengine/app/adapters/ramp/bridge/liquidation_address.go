package bridge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ProvisionLiquidationAddress returns the address that pays req's bank account
// over req's rail from req's token and chain, creating one only when none
// exists.
//
// Read-then-create, as for a virtual account. The sandbox refuses a second
// create for the same (external account, chain, currency, rail) with 400, but
// that refusal is a 502 to the app unless handled, so the listing comes first
// and a 400 invalid_parameters on the create re-reads the route. ACH and wire
// to the same account are different routes (both 201 in the sandbox). The
// service serializes the create under its own lock.
func (a *Adapter) ProvisionLiquidationAddress(
	ctx context.Context,
	ref ramp.CustomerRef,
	req ramp.LiquidationAddressRequest,
) (*ramp.LiquidationAddress, error) {
	if err := refuseCurrency(req.Source.Currency); err != nil {
		return nil, err
	}
	rail, err := bridgePayoutRail(req.Rail)
	if err != nil {
		return nil, err
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.ExternalAccountID == "" {
		return nil, errors.New("bridge: a liquidation address needs the external account it pays")
	}
	currency := normalizeCurrency(req.Source.Currency)
	chain := req.Source.PaymentRail

	existing, err := a.findLiquidationAddress(ctx, ref, req.ExternalAccountID, rail, chain, currency)
	if err != nil {
		return nil, ramp.NotSent(err)
	}
	if existing != nil {
		return existing, nil
	}

	body := liquidationAddressRequest{
		Chain:                     chain,
		Currency:                  currency,
		ExternalAccountID:         req.ExternalAccountID,
		DestinationPaymentRail:    rail,
		DestinationCurrency:       usdCurrency,
		CustomDeveloperFeePercent: req.FeePercent,
	}
	if req.ReturnAddress != "" {
		body.ReturnInstructions = &liquidationReturnInstruction{Address: req.ReturnAddress}
	}
	if rail == bridgeRailWire {
		body.DestinationWireMessage = req.Reference
	} else {
		body.DestinationACHReference = req.Reference
	}

	resp, raw, err := ramp.Call[liquidationAddressResponse](ctx, a.do, Request{
		Method:         http.MethodPost,
		Path:           liquidationAddressesPath(ref.ExternalID),
		Body:           body,
		IdempotencyKey: liquidationIdempotencyKey(ref, req, rail, chain, currency),
	})
	if err != nil {
		// Bridge keeps one address per (external account, chain, currency,
		// rail) and refuses a second with 400 invalid_parameters (the message,
		// which UpstreamError never keeps, says "already exists"). A create
		// that raced another lands here: read again and return the address.
		var upstream *UpstreamError
		if errors.As(err, &upstream) && upstream.Status == http.StatusBadRequest &&
			upstream.Code == "invalid_parameters" {
			if again, findErr := a.findLiquidationAddress(ctx, ref, req.ExternalAccountID, rail, chain, currency); findErr == nil && again != nil {
				return again, nil
			}
		}
		return nil, err
	}
	return liquidationAddressFrom(resp, raw)
}

// UpdateLiquidationAddress changes the reference, the fee or the return
// address (return_instructions) of an address.
//
// The address is read first: the reference lives in the field of its rail, and
// the rail is Bridge's to report. The PUT carries only what changes, with no
// idempotency key (the sandbox refuses one on a PUT with 422); an empty
// reference is sent as null, which clears it ("" is refused with 400).
func (a *Adapter) UpdateLiquidationAddress(
	ctx context.Context,
	ref ramp.CustomerRef,
	addressID string,
	req ramp.LiquidationAddressUpdate,
) (*ramp.LiquidationAddress, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if addressID == "" {
		return nil, errors.New("bridge: update liquidation address requires an address id")
	}

	current, raw, err := ramp.Call[liquidationAddressResponse](ctx, a.do, Request{
		Method: http.MethodGet,
		Path:   liquidationAddressPath(ref.ExternalID, addressID),
	})
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if req.Reference != nil {
		key := "destination_ach_reference"
		if current.DestinationPaymentRail == bridgeRailWire {
			key = "destination_wire_message"
		}
		var value any
		if *req.Reference != "" {
			value = *req.Reference
		}
		body[key] = value
	}
	if req.FeePercent != "" {
		body["custom_developer_fee_percent"] = req.FeePercent
	}
	if req.ReturnAddress != "" {
		body["return_instructions"] = liquidationReturnInstruction{Address: req.ReturnAddress}
	}
	if len(body) == 0 {
		return liquidationAddressFrom(current, raw)
	}

	updated, raw, err := ramp.Call[liquidationAddressResponse](ctx, a.do, Request{
		Method: http.MethodPut,
		Path:   liquidationAddressPath(ref.ExternalID, addressID),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}
	return liquidationAddressFrom(updated, raw)
}

// findLiquidationAddress returns the customer's reusable address for the
// route, or nil when there is none. Only `deactivated` is skipped, for the
// reason findVirtualAccount gives: a state we do not recognise is a visible
// problem, a second address a silent one.
func (a *Adapter) findLiquidationAddress(
	ctx context.Context,
	ref ramp.CustomerRef,
	externalAccountID, rail, chain, currency string,
) (*ramp.LiquidationAddress, error) {
	var list liquidationAddressList
	if err := a.do(ctx, Request{
		Method: http.MethodGet,
		Path:   liquidationAddressesPath(ref.ExternalID),
		Query:  limitQuery(liquidationAddressPageLimit),
	}, &list); err != nil {
		return nil, err
	}

	for _, raw := range list.Data {
		var listed liquidationAddressResponse
		if err := json.Unmarshal(raw, &listed); err != nil {
			return nil, fmt.Errorf("bridge: decode listed liquidation address: %w", err)
		}
		if listed.State == bridgeLiquidationAddressDeactivated ||
			listed.ExternalAccountID != externalAccountID ||
			listed.Chain != chain ||
			normalizeCurrency(listed.Currency) != currency ||
			cmp.Or(listed.DestinationPaymentRail, bridgeRailACH) != rail {
			continue
		}
		return liquidationAddressFrom(listed, raw)
	}
	return nil, nil
}

// bridgePayoutRail names the bank rail of a USD payout rail. Any other rail
// has no liquidation address behind it.
func bridgePayoutRail(rail ramp.Rail) (string, error) {
	switch rail {
	case ramp.RailUSDACHPayout:
		return bridgeRailACH, nil
	case ramp.RailUSDWirePayout:
		return bridgeRailWire, nil
	}
	return "", fmt.Errorf("bridge: rail %q has no liquidation address: %w", rail, ramp.ErrNoProviderAvailable)
}

// railFromBridge is bridgePayoutRail's inverse. Bridge defaults an address
// with no rail to ACH, and so does this.
func railFromBridge(rail string) ramp.Rail {
	if rail == bridgeRailWire {
		return ramp.RailUSDWirePayout
	}
	return ramp.RailUSDACHPayout
}

// liquidationAddressFrom normalizes a created, fetched or listed address.
func liquidationAddressFrom(resp liquidationAddressResponse, raw json.RawMessage) (*ramp.LiquidationAddress, error) {
	if resp.ID == "" || resp.Address == "" {
		return nil, errors.New("bridge: liquidation address has no id or no address")
	}
	status := models.LiquidationAddressStatusInactive
	if resp.State == bridgeLiquidationAddressActive {
		status = models.LiquidationAddressStatusActive
	}
	returnAddress := ""
	if resp.ReturnInstructions != nil {
		returnAddress = resp.ReturnInstructions.Address
	}
	return &ramp.LiquidationAddress{
		ReturnAddress:     returnAddress,
		ProviderAddressID: resp.ID,
		Address:           resp.Address,
		Status:            status,
		Rail:              railFromBridge(resp.DestinationPaymentRail),
		Chain:             resp.Chain,
		Currency:          normalizeCurrency(resp.Currency),
		ExternalAccountID: resp.ExternalAccountID,
		Reference:         cmp.Or(resp.DestinationACHReference, resp.DestinationWireMessage),
		FeePercent:        resp.CustomDeveloperFeePercent,
		Raw:               raw,
	}, nil
}

func liquidationAddressesPath(customerID string) string {
	return customerPath(customerID) + "/liquidation_addresses"
}

func liquidationAddressPath(customerID, addressID string) string {
	return liquidationAddressesPath(customerID) + "/" + addressID
}

// liquidationIdempotencyKey prefers the caller's key and derives one from the
// route otherwise: a retried timeout must reach Bridge as the same create.
func liquidationIdempotencyKey(
	ref ramp.CustomerRef,
	req ramp.LiquidationAddressRequest,
	rail, chain, currency string,
) string {
	if req.IdempotencyKey != "" {
		return req.IdempotencyKey
	}
	return strings.Join([]string{"la", ref.ExternalID, req.ExternalAccountID, rail, chain, currency}, ":")
}
