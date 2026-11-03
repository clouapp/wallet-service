package ramps

import (
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrWalletNotRegistered refuses a business's ramp whose own address — where
// an on-ramp delivers, what an off-ramp sends from — is not one the business
// registered (spec §9.1, §9.3; API contract §10).
var ErrWalletNotRegistered = errors.New("ramps: wallet not registered")

// ErrHolderMismatch refuses a business's payout to any key but the company's
// own CNPJ (spec §9.2, §18.8): another company's, or a key of another type,
// whose holder document is only what the user typed. One code for both: the
// app says the same thing either way.
var ErrHolderMismatch = errors.New("ramps: holder mismatch")

// errBusinessNotActive refuses the money of a company that is not active: a
// draft, approved at no provider yet — or approved, and kept a draft because
// another company holds its document. It is the refusal the app already opens
// the company's KYB with (kyc_pending, API contract §10).
var errBusinessNotActive = fmt.Errorf("ramps: business not active: %w", ramp.ErrKycPending)

// cnpjPixKey is the Pix key type whose key is a company's CNPJ itself.
const cnpjPixKey = "cnpj"

// holderRules are the money rules a business customer is held to before any
// provider is asked (spec §9), in the contract's order (§10): after the local
// gates the order already passed, it is active, then the address it moves
// money to or from is one it registered, then — for a payout — the key is its
// own CNPJ. A person is never asked: their rules are the ones every order
// already passes.
type holderRules struct {
	wallets    repositories.CustomerWalletRepository
	businesses repositories.CustomerBusinessProfileRepository
	activation BusinessActivation
}

// BusinessActivation reconciles a business draft's status with its verdicts:
// approved at a provider, no other business holding its document, it is
// activated, and answers true (customers.KycVerdicts).
type BusinessActivation interface {
	ActivateIfApproved(customerID uint) (bool, error)
}

// onramp is what a business's deposit to wallet on chain owes.
func (r holderRules) onramp(customer *models.Customer, chain, wallet string) error {
	if !customer.IsBusiness() {
		return nil
	}
	if err := activeBusiness(customer, r.activation); err != nil {
		return err
	}
	return r.ownWallet(customer, chain, wallet)
}

// offramp is what a business's payout from fromAddress on chain to the Pix
// key owes.
func (r holderRules) offramp(customer *models.Customer, chain, fromAddress, keyType, key string) error {
	if !customer.IsBusiness() {
		return nil
	}
	if err := activeBusiness(customer, r.activation); err != nil {
		return err
	}
	if err := r.ownWallet(customer, chain, fromAddress); err != nil {
		return err
	}
	return ownPixKey(customer, keyType, key)
}

// bankOfframp is what a business's bank payout from fromAddress on chain
// owes: it is active, and the address is its own. There is no key rule — the
// destination is a beneficiary the customer registered, not a Pix key.
func (r holderRules) bankOfframp(customer *models.Customer, chain, fromAddress string) error {
	if !customer.IsBusiness() {
		return nil
	}
	if err := activeBusiness(customer, r.activation); err != nil {
		return err
	}
	return r.ownWallet(customer, chain, fromAddress)
}

// holder is who a payout's key belongs to: the name the request carries for
// a person; for a company, the legal name it declared — the key is its own
// CNPJ (ownPixKey), and the provider records the holder XIP sends, never the
// one typed (API contract §10).
func (r holderRules) holder(customer *models.Customer, named string) (string, error) {
	if !customer.IsBusiness() {
		return named, nil
	}
	profiles, err := r.businesses.FindByCustomers([]uint{customer.ID})
	if err != nil {
		return "", fmt.Errorf("ramps: read the company's declaration: %w", err)
	}
	legal := strings.TrimSpace(profiles[customer.ID].LegalName)
	if legal == "" {
		return "", fmt.Errorf("ramps: business %d declared no legal name to pay out to", customer.ID)
	}
	return legal, nil
}

// activeBusiness refuses a company that is not active: it moves no money. A
// draft is reconciled first (activation): approved at a provider, its
// activation may have failed when the verdict came, or the document a rival
// held may have been released since, and no later verdict would activate
// it. Activated, the rest of the request reads it active.
func activeBusiness(customer *models.Customer, activation BusinessActivation) error {
	if customer.Status == models.CustomerStatusActive {
		return nil
	}
	if customer.Status != models.CustomerStatusDraft || activation == nil {
		return errBusinessNotActive
	}
	activated, err := activation.ActivateIfApproved(customer.ID)
	if err != nil {
		return fmt.Errorf("ramps: reconcile business %d: %w", customer.ID, err)
	}
	if !activated {
		return errBusinessNotActive
	}
	customer.Status = models.CustomerStatusActive
	return nil
}

// ownWallet refuses an address the business did not register on chain. The
// address is trimmed as registration trims it, and then compared exactly:
// base58 tells upper from lower case.
func (r holderRules) ownWallet(customer *models.Customer, chain, address string) error {
	if !customer.IsBusiness() {
		return nil
	}
	wallet, err := r.wallets.FindByAddress(chain, strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("ramps: read the wallet registry: %w", err)
	}
	if wallet == nil || wallet.CustomerID != customer.ID {
		return ErrWalletNotRegistered
	}
	return nil
}

// ownPixKey refuses a business's payout to a key that is not its own CNPJ:
// the key, in its canonical form, has to digest to the document the company
// is held by (repositories.BusinessTaxIDDigest, the digest that wrote
// customers.tax_id_hash). A business holding no document — one from abroad
// has no CNPJ — has no key of its own.
func ownPixKey(customer *models.Customer, keyType, key string) error {
	if !customer.IsBusiness() {
		return nil
	}
	if keyType != cnpjPixKey || customer.TaxIDHash == nil {
		return ErrHolderMismatch
	}
	identity := models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}
	if repositories.BusinessTaxIDDigest(identity, key) != *customer.TaxIDHash {
		return ErrHolderMismatch
	}
	return nil
}

// refusedCustomer reports whether err is a money rule's refusal.
func refusedCustomer(err error) bool {
	return errors.Is(err, errBusinessNotActive) || errors.Is(err, ErrWalletNotRegistered) ||
		errors.Is(err, ErrHolderMismatch)
}

// failoverUnlessRefused is the estimates' failover policy: any failure is
// retried on the next provider, except a money rule's refusal, which is about
// the customer — the next provider would hear the same answer.
func failoverUnlessRefused(err error) bool {
	return ramp.FailoverOnAnyError(err) && !refusedCustomer(err)
}

// failoverUnlessPixAccountRefused is the same for a Pix quote that may be a
// provider's prepared funding: a destination the provider is still verifying,
// or refused, is the customer's to resolve, so the next provider is not asked
// to hide it.
func failoverUnlessPixAccountRefused(err error) bool {
	return failoverUnlessRefused(err) &&
		!errors.Is(err, ramp.ErrBankAccountVerifying) && !errors.Is(err, ramp.ErrBankAccountRejected)
}
