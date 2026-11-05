package ozav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// travelRule is the BCB Resolution 520 record, only the members Ozav requires
// on an external crypto send.
type travelRule struct {
	Originator  travelRuleParty `json:"originator"`
	Beneficiary travelRuleParty `json:"beneficiary"`
}

type travelRuleParty struct {
	Name              string               `json:"name"`
	Account           string               `json:"account,omitempty"`
	IdentityDocuments []travelRuleDocument `json:"identity_documents,omitempty"`
	Address           *travelRuleAddress   `json:"address,omitempty"`
	WalletType        string               `json:"wallet_type,omitempty"`
	AttestedAt        string               `json:"wallet_attested_ownership_at,omitempty"`
	// IsSelf is a pointer so an explicit false is sent: the field is required
	// here, and absent is not the same statement as no.
	IsSelf *bool `json:"is_self,omitempty"`
}

type travelRuleDocument struct {
	Type           string `json:"type"`
	Number         string `json:"number"`
	IssuingCountry string `json:"issuing_country"`
}

// travelRuleAddress writes street_line_1, the canonical key (street and line1
// are aliases).
type travelRuleAddress struct {
	StreetLine1 string `json:"street_line_1"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city"`
	State       string `json:"state,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	Country     string `json:"country"`
}

// walletTypeSelfCustodied is the beneficiary: the user's own wallet. `hosted`
// is refused on an external send.
const walletTypeSelfCustodied = "self_custodied"

// transferAmountRequest is an object: a decimal string is a measured 400.
type transferAmountRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// deliveryTransferRequest is a same-asset send: `amount` and no `quote_id`.
type deliveryTransferRequest struct {
	CustomerID  string                `json:"customer_id"`
	Amount      transferAmountRequest `json:"amount"`
	Source      transferSource        `json:"source"`
	Destination transferDestination   `json:"destination"`
	TravelRule  travelRule            `json:"travel_rule"`
}

const destinationCryptoAddress = "crypto_address"

// originatorFrom turns the registered customer into the Travel Rule's
// originator, refusing a partial record: Ozav would answer 422 about a field
// the user never saw, where the real problem is an incomplete KYC record.
func originatorFrom(customer *customerResponse) (travelRuleParty, error) {
	individual := customer.Individual
	if individual == nil {
		return travelRuleParty{}, fmt.Errorf(
			"ozav: customer %s has no individual record to build a travel rule from", customer.ID)
	}
	if individual.LegalName == "" {
		return travelRuleParty{}, fmt.Errorf("ozav: customer %s has no legal name", customer.ID)
	}
	if individual.TaxID == nil || individual.TaxID.Value == "" {
		return travelRuleParty{}, fmt.Errorf(
			"ozav: customer %s has no tax id — Resolution 520 requires a cpf or cnpj on the originator",
			customer.ID)
	}
	address := individual.ResidentialAddress
	if address == nil || address.Line1 == "" || address.City == "" || address.Country == "" {
		return travelRuleParty{}, fmt.Errorf(
			"ozav: customer %s has no complete residential address", customer.ID)
	}

	country := individual.TaxID.Country
	if country == "" {
		country = taxIDCountryBR
	}
	return travelRuleParty{
		Name: individual.LegalName,
		IdentityDocuments: []travelRuleDocument{{
			Type: individual.TaxID.Type, Number: individual.TaxID.Value, IssuingCountry: country,
		}},
		Address: &travelRuleAddress{
			StreetLine1: address.Line1, City: address.City, State: address.State,
			PostalCode: address.PostalCode, Country: address.Country,
		},
	}, nil
}

// DeliverOnramp sends an on-ramp's crypto from the customer's Ozav wallet to the
// user's own. It is a second movement by construction: a pay-in can only land
// in the Ozav wallet (fiat_payment -> crypto_address is a measured 422), and a
// customer holds exactly one wallet, of the custody fixed at creation.
//
// Leaving custody for an external address needs a Travel Rule record (422
// TRAVEL_RULE_REQUIRED without one, measured 31/08). The originator is read
// back from the customer Ozav holds rather than carried down from the service:
// the same data XIP registered, kept out of a layer with no other use for it.
func (a *Adapter) DeliverOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampDeliveryRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.Destination.Address == "" {
		return nil, errors.New("ozav: deliver onramp requires the user's destination address")
	}

	var customer customerResponse
	if err := a.do(ctx, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)}, &customer); err != nil {
		return nil, err
	}
	originator, err := originatorFrom(&customer)
	if err != nil {
		return nil, err
	}

	walletID, err := a.resolveWallet(ctx, ref.ExternalID)
	if err != nil {
		return nil, err
	}

	// The amount arrives with numeric(38,18)'s eighteen decimals; Ozav takes
	// six (a measured 400).
	amount, err := ramp.ScaleAmount(req.Amount, cryptoScale)
	if err != nil {
		return nil, fmt.Errorf("ozav: deliver onramp amount: %w", err)
	}

	currency := quoteCurrency(req.Destination.Currency)
	isSelf := true
	src := transferSource{Type: sourceWallet, WalletID: walletID, Currency: currency}
	chain := normalizeVocabulary(req.Destination.PaymentRail)
	if chain != "" {
		src.Chain = chain
	}
	body := deliveryTransferRequest{
		CustomerID: ref.ExternalID,
		Amount:     transferAmountRequest{Amount: amount, Currency: currency},
		Source:     src,
		Destination: transferDestination{
			Type:     destinationCryptoAddress,
			Address:  req.Destination.Address,
			Network:  chain,
			Currency: currency,
		},
		TravelRule: travelRule{
			Originator: originator,
			Beneficiary: travelRuleParty{
				// The same person on both sides: is_self.
				Name:       originator.Name,
				Account:    req.Destination.Address,
				WalletType: walletTypeSelfCustodied,
				AttestedAt: req.AttestedOwnershipAt.UTC().Format(time.RFC3339),
				IsSelf:     &isSelf,
			},
		},
	}

	order, _, err := a.postTransfer(ctx, body, req.IdempotencyKey)
	return order, err
}

var _ ramp.OnrampDeliveryAdapter = (*Adapter)(nil)
