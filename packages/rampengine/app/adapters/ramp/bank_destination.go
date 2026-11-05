package ramp

import "fmt"

// BankDestination is where a bank payout lands: the provider-side account the
// beneficiary was registered as.
//
// It carries the provider's id and nothing else. The routing and account
// numbers reach a provider only through the registration call, so a quote or a
// create — and every log line and retry around them — never holds them.
type BankDestination struct {
	// Rail is the rail the account was registered for. A provider registers an
	// ACH account and a wire account as two objects, so an id is only good on
	// the rail it was made for.
	Rail Rail
	// Currency is the fiat the account receives, lowercase ("usd").
	Currency string
	// ExternalAccountID is the provider's id for the registered account.
	ExternalAccountID string
}

// BankPayoutAccount is a provider bank account approved for a beneficiary:
// what a bank payout quote pays out to and snapshots, so a create can tell a
// beneficiary edited or an account replaced since. The bank service answers it
// (bank.ProviderAccount is this type) and the ramps service consumes it.
type BankPayoutAccount struct {
	// ExternalAccountID is the provider's id for the account (ba_).
	ExternalAccountID string
	// BeneficiaryRevision is the beneficiary revision it was registered for.
	BeneficiaryRevision int
	// CustomerExternalID is the provider customer it belongs to.
	CustomerExternalID string
}

// ValidateDestination holds the request's destination to the rail it is for.
//
// A bank rail needs a registered account for that same rail and no Pix key; a
// pix rail needs no bank account. Adapters are written against one shape or the
// other, and a request carrying both would let whichever field an adapter reads
// first decide where the money goes.
func (r OfframpQuoteRequest) ValidateDestination(rail Rail) error {
	if !rail.IsBank() {
		if r.Bank != nil {
			return fmt.Errorf("%w: %s carries a bank account", ErrInvalidDestination, rail)
		}
		return nil
	}
	if r.Bank == nil || r.Bank.ExternalAccountID == "" {
		return fmt.Errorf("%w: %s needs a registered bank account", ErrInvalidDestination, rail)
	}
	if r.Bank.Rail != rail {
		return fmt.Errorf("%w: account registered for %s, not %s", ErrInvalidDestination, r.Bank.Rail, rail)
	}
	if r.Destination != (PixDestination{}) {
		return fmt.Errorf("%w: %s also carries a pix key", ErrInvalidDestination, rail)
	}
	return nil
}
