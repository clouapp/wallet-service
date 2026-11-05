// Package blindpay is the BlindPay provider adapter: an HTTP mapper between
// the ramp contract and BlindPay's /v1 API. It never touches the database; the
// service resolves a ramp.CustomerRef and passes it in.
//
// It implements KYC (terms, the atomic customer create, the KYC reads and
// webhooks) and the Pix on-ramp: Supports opens pix_payin, and the payin is
// quoted, created and read here. The bank payout is quoted (EstimateOfframp)
// and the customer's remaining payout limit read (RemainingPayoutUSD); only
// CreateOfframp still refuses with ErrProviderNotImplemented. It joins the Pix wizard's
// fan-out only for a person whose declaration opens it a Pix rail
// (ramp.PixByDeclaredResidence).
package blindpay

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// SecretFactory resolves the Svix signing secret from the current settings on
// every delivery, so rotating it in the admin takes effect without a restart.
type SecretFactory func(ctx context.Context) (string, error)

// ClientFactory resolves a Client from the current settings on every call, so
// an admin rotating the key or the instance takes effect without a restart.
// An error is always a configuration gap found before any request.
type ClientFactory func(ctx context.Context) (*Client, error)

// PayoutGate says whether the instance can pay out the named stablecoin
// ("USDC" or "USDT") today: credentials, mint and, in production, the
// operator's confirmation. A nil gate answers false for every token.
type PayoutGate func(token string) bool

// Adapter is BlindPay behind the ramp contract.
type Adapter struct {
	clients ClientFactory
	secrets SecretFactory
	payouts PayoutGate
	// partnerFees caches partner fee profile ids by name, per process.
	partnerFees sync.Map
	// wallets caches blockchain wallet ids by instance, customer, network and
	// address, per process.
	wallets sync.Map
	// now is the clock the find-or-create keys are bucketed by.
	now func() time.Time
	// feeFlight and walletFlight share one find-or-create among concurrent
	// first estimates for the same cache key. Without them each would POST
	// under the same Idempotency-Key, and BlindPay answers all but one with
	// 409 idempotency_key_in_flight, a retryable error for that user.
	feeFlight    singleflight.Group
	walletFlight singleflight.Group
}

// New builds the adapter. Any of the three may be nil in a test that never uses
// it; a call that needs one then fails as unconfigured.
func New(clients ClientFactory, secrets SecretFactory, payouts PayoutGate) *Adapter {
	return &Adapter{clients: clients, secrets: secrets, payouts: payouts, now: time.Now}
}

// createKey is the Idempotency-Key of a find-or-create POST: base plus the
// UTC hour. BlindPay replays a key for 24h, so a key without the hour would
// keep answering the id of a wallet or fee profile deleted on the dashboard
// for that long. The GET before every POST is what keeps an hour boundary from
// creating a duplicate; the key only covers two creates racing within one.
func (a *Adapter) createKey(base string) string {
	return base + ":" + a.now().UTC().Format("2006010215")
}

// client resolves the transport for one call. Every failure here happened
// before a request existed, which is what ErrRequestNotSent promises a caller.
func (a *Adapter) client(ctx context.Context) (*Client, error) {
	if a.clients == nil {
		return nil, fmt.Errorf("blindpay: no client factory: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.clients(ctx)
	if err != nil {
		return nil, fmt.Errorf("blindpay: %w: %w", ramp.ErrRequestNotSent, err)
	}
	return client, nil
}

// Code names the provider.
func (*Adapter) Code() ramp.ProviderCode { return ramp.ProviderBlindPay }

// PreferredKycMode is API documents: BlindPay takes the customer's documents through its API.
func (*Adapter) PreferredKycMode() ramp.KycMode { return ramp.KycModeAPIDocs }

// RequiresTos is true: customers must accept BlindPay's own terms before a customer exists.
func (*Adapter) RequiresTos() bool { return true }

// Supports opens pix_payin for USDC/USDT on Solana, and the ach, wire and pix
// payouts of the same tokens once the payout gate says the instance is ready.
// An unnamed token or chain means the caller did not ask — the sticky create
// path resolves on the rail alone, the same rule Bridge and Ozav follow. An
// unnamed token on a bank rail asks "any token": true if USDC or USDT is ready.
func (a *Adapter) Supports(c ramp.Capability) bool {
	bank := c.Rail == ramp.RailACHPayout || c.Rail == ramp.RailWirePayout
	// A Pix payout is served like a bank payout: the same token programs and
	// the same payout gate, because it is funded by the same prepared action.
	payout := bank || c.Rail == ramp.RailPixPayout
	if c.Rail != ramp.RailPixPayin && !payout {
		return false
	}
	if c.Token != "" && !strings.EqualFold(c.Token, "USDC") && !strings.EqualFold(c.Token, "USDT") {
		return false
	}
	if c.Chain != "" && !strings.EqualFold(c.Chain, "solana") {
		return false
	}
	if !payout {
		return true
	}
	if a.payouts == nil {
		return false
	}
	if c.Token == "" {
		return a.payouts("USDC") || a.payouts("USDT")
	}
	return a.payouts(strings.ToUpper(c.Token))
}

// CreateHostedKycLink refuses: BlindPay collects the documents through its API.
func (*Adapter) CreateHostedKycLink(context.Context, ramp.CustomerRef, ramp.HostedLinkOpts) (*ramp.HostedKycLink, error) {
	return nil, ramp.ErrProviderNotImplemented
}

// EstimateOnramp, CreateOnramp and GetOrder live in payin.go, EstimateOfframp
// and RemainingPayoutUSD in quote.go and limits.go, PrepareOfframp and
// ValidateSignedOfframpAction in prepare.go. CreateOfframp refuses until its
// stage is built.

func (*Adapter) CreateOfframp(context.Context, ramp.CustomerRef, ramp.OfframpCreateRequest) (*ramp.RampOrder, error) {
	return nil, ramp.ErrProviderNotImplemented
}

// shareFlight runs fn once per key among concurrent callers.
//
// The flight runs on a context detached from every caller's cancellation: if it
// ran on the first caller's, that caller giving up would fail the request for
// all the others waiting on it. Each caller instead waits on its own ctx and
// leaves alone, while the flight finishes for the rest (and fills the cache).
func shareFlight(ctx context.Context, g *singleflight.Group, key string, fn func(context.Context) (string, error)) (string, error) {
	flight := context.WithoutCancel(ctx)
	ch := g.DoChan(key, func() (any, error) { return fn(flight) })
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
