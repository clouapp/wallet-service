package chainregistry

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// PrimaryVerifier asks an endpoint which network it serves before it becomes the
// primary (chains.rpc_url) of a chain, for chains:set-rpc and
// PATCH /v1/platform/chains/{chainId}/rpc.
type PrimaryVerifier struct {
	open    func(stored string) (string, error)
	profile func() string
}

// profileName is the configured profile, or "" without one.
func (v *PrimaryVerifier) profileName() string {
	if v == nil || v.profile == nil {
		return ""
	}
	return v.profile()
}

// NewPrimaryVerifier builds the verifier. open turns a stored rpc_url into the URL
// it dials (it decides the Solana cluster the chain is on now); profile is
// CHAIN_NETWORK_PROFILE. Either may be nil.
func NewPrimaryVerifier(open func(stored string) (string, error), profile func() string) *PrimaryVerifier {
	return &PrimaryVerifier{open: open, profile: profile}
}

// Verify returns the network candidate (a URL or env:NAME) serves when it is the
// chain's own network, or, for a primary record (eth, sol, ...), the network the
// configured profile names for it: the move chains:align-network makes next, then
// moved is true. Another network is chain.ErrWrongNetwork and an endpoint that does
// not answer is ErrEndpointUnverified. A chain whose endpoints cannot be asked
// (Bitcoin family, TRON, XRP) is ErrEndpointIdentityUnsupported: nothing is checked.
func (v *PrimaryVerifier) Verify(ctx context.Context, record models.Chain, candidate string) (network string, moved bool, err error) {
	if !models.RPCFallbacksSupported(record.AdapterType) {
		return "", false, ErrEndpointIdentityUnsupported
	}
	dial, err := models.DialEndpoint(candidate)
	if err != nil {
		return "", false, fmt.Errorf("%w: the endpoint does not resolve in this process (an env:NAME must name a variable set here)", ErrEndpointUnverified)
	}
	current := ""
	if v != nil && v.open != nil {
		if opened, openErr := v.open(record.RpcURL); openErr == nil {
			current = opened
		}
	}
	own := ChainEndpointTarget(record, current)
	goal, profiled := ProfileEndpointTarget(v.profileName(), record)
	if record.AdapterType == models.AdapterTypeEVM && own.EVMNetworkID <= 0 {
		// Without network_id there is nothing of the chain's own to compare with:
		// the profile's chain id stands in (align-network writes it next), else the
		// refusal says how to set it.
		if !profiled || goal.EVMNetworkID <= 0 {
			return "", false, fmt.Errorf("%w: chain %s has no network_id, so eth_chainId cannot be compared: "+
				"set it (chains:align-network --apply sets it on a primary record, from CHAIN_NETWORK_PROFILE) or, "+
				"from artisan only, pass --skip-network-check", ErrEndpointUnverified, record.ID)
		}
		network, err = VerifyEndpoint(ctx, record.AdapterType, goal, dial)
		return network, err == nil, err
	}
	network, err = VerifyEndpoint(ctx, record.AdapterType, own, dial)
	if err == nil || !errors.Is(err, chain.ErrWrongNetwork) || !profiled {
		return network, false, err
	}
	if _, profileErr := VerifyEndpoint(ctx, record.AdapterType, goal, dial); profileErr != nil {
		return network, false, err
	}
	return network, true, nil
}
