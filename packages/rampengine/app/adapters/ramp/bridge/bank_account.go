package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// usdCurrency is the fiat a US bank account is paid in.
const usdCurrency = "usd"

// RegisterBankAccount registers the user's US bank account as a payout
// destination.
//
// There is no read-then-create, for the reason ensureExternalAccount gives:
// Bridge answers with the account number masked (last_4), so a listing can
// never match the account we hold. The caller prevents a second registration:
// it records the id this returns in `bank_account_destinations` and reuses it.
func (a *Adapter) RegisterBankAccount(
	ctx context.Context,
	ref ramp.CustomerRef,
	req ramp.BankAccountRegistration,
) (*ramp.RegisteredBankAccount, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.RoutingNumber == "" || req.AccountNumber == "" {
		return nil, errors.New("bridge: a bank account needs a routing number and an account number")
	}

	body := usExternalAccountRequest{
		Currency:         usdCurrency,
		AccountType:      usAccountType,
		BankName:         req.BankName,
		AccountName:      req.AccountName,
		AccountOwnerType: customerTypeIndividual,
		AccountOwnerName: req.OwnerName,
		FirstName:        req.OwnerFirstName,
		LastName:         req.OwnerLastName,
		Account: usBankAccountPayload{
			RoutingNumber:     req.RoutingNumber,
			AccountNumber:     req.AccountNumber,
			CheckingOrSavings: req.AccountType,
		},
		Address: externalAccountAddress{
			StreetLine1: req.Address.StreetLine1,
			StreetLine2: req.Address.StreetLine2,
			City:        req.Address.City,
			State:       req.Address.State,
			PostalCode:  req.Address.PostalCode,
			Country:     req.Address.Country,
		},
	}

	resp, raw, err := ramp.Call[usExternalAccountResponse](ctx, a.do, Request{
		Method:         http.MethodPost,
		Path:           customerPath(ref.ExternalID) + "/external_accounts",
		Body:           body,
		IdempotencyKey: bankAccountIdempotencyKey(ref, req),
	})
	if err != nil {
		return nil, err
	}
	if resp.ID == "" {
		return nil, errors.New("bridge: external account created without an id")
	}

	registered := &ramp.RegisteredBankAccount{ExternalAccountID: resp.ID, Raw: raw}
	if len(resp.PaymentRails.Supported) > 0 {
		registered.PaymentRails = resp.PaymentRails.Supported
	}
	return registered, nil
}

// DeactivateBankAccount deactivates the external account a deleted bank
// account was registered as. Bridge offers no deactivate for a liquidation
// address (its update takes no state), so this is what stops the account's
// addresses paying it. The external account is kept and could be reactivated.
func (a *Adapter) DeactivateBankAccount(ctx context.Context, ref ramp.CustomerRef, externalAccountID string) error {
	if ref.ExternalID == "" {
		return ramp.ErrIntegrationRequired
	}
	if externalAccountID == "" {
		return errors.New("bridge: deactivating a bank account needs its external account id")
	}
	// The answer is the account object; nothing of it is kept.
	return a.do(ctx, Request{
		Method:         http.MethodPost,
		Path:           customerPath(ref.ExternalID) + "/external_accounts/" + url.PathEscape(externalAccountID) + "/deactivate",
		IdempotencyKey: "ext-deactivate:" + externalAccountID,
	}, nil)
}

// bankAccountIdempotencyKey prefers the caller's key. The derived one hashes
// the numbers: the header travels through proxies and logs, the numbers must
// not.
func bankAccountIdempotencyKey(ref ramp.CustomerRef, req ramp.BankAccountRegistration) string {
	if req.IdempotencyKey != "" {
		return req.IdempotencyKey
	}
	sum := sha256.Sum256([]byte(req.RoutingNumber + ":" + req.AccountNumber))
	return "ext-acct-us:" + ref.ExternalID + ":" + hex.EncodeToString(sum[:])[:16]
}
