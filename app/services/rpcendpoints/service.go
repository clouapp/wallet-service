// Package rpcendpoints manages the ordered RPC endpoints of a chain: the primary
// (chains.rpc_url, position 1) and its fallbacks (chain_rpc_endpoints, positions 2
// and after). It lists them with their health, and adds, removes, reorders,
// enables, disables or replaces them after asking each new endpoint which network
// it serves. URLs are sealed at rest and never printed, logged or returned: an
// endpoint is shown by its host and a fingerprint.
package rpcendpoints

import (
	"context"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

// ChainStore reads a chain and writes its sealed primary endpoint.
type ChainStore interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
	UpdateRPCURL(ctx context.Context, id, sealed string) error
}

// EndpointStore reads and replaces the sealed fallbacks of a chain. Within runs fn
// in one transaction the chain store joins through the context.
type EndpointStore interface {
	ListByChain(ctx context.Context, chainID string) ([]models.ChainRPCEndpoint, error)
	Replace(ctx context.Context, chainID string, endpoints []models.ChainRPCEndpoint) error
	Within(ctx context.Context, fn func(context.Context) error) error
}

// Sealer seals an endpoint for storage and opens a stored one. The plaintext is
// never written into an error.
type Sealer interface {
	Seal(plaintext string) (string, error)
	Open(stored string) (string, error)
}

// HealthReader reads the endpoint health the running processes recorded.
type HealthReader interface {
	RPCHealth(ctx context.Context, chainID string) (chain.RPCHealthSnapshot, bool, error)
}

// Dialer points the chain adapters of this process at a new list. A chain that is
// not loaded answers false.
type Dialer interface {
	ReplaceEndpoints(chainID, primary string, fallbacks []string) (bool, error)
}

// VerifyFunc asks rawURL which network it serves (chainregistry.VerifyEndpoint).
type VerifyFunc func(ctx context.Context, adapterType string, target chainregistry.EndpointTarget, rawURL string) (string, error)

// ProbeFunc asks rawURL its network and head (chainregistry.ProbeEndpoint).
type ProbeFunc func(ctx context.Context, adapterType string, target chainregistry.EndpointTarget, rawURL string) chainregistry.EndpointProbe

// Deps is everything the service needs. Chains, Endpoints and Sealer are required.
// Verify and Probe default to the chainregistry calls and Defaults to
// DefaultFallbacks. Health and Dialer are optional: without them the list shows
// no health and a change is picked up when the processes restart.
type Deps struct {
	Chains    ChainStore
	Endpoints EndpointStore
	Sealer    Sealer
	Verify    VerifyFunc
	Probe     ProbeFunc
	Health    HealthReader
	Dialer    Dialer
	Defaults  func(network string) []string
	Now       func() time.Time
}

// Service lists and changes the RPC endpoints of a chain.
type Service struct {
	chains    ChainStore
	endpoints EndpointStore
	sealer    Sealer
	verify    VerifyFunc
	probe     ProbeFunc
	health    HealthReader
	dialer    Dialer
	defaults  func(network string) []string
	now       func() time.Time
}

// NewService builds the endpoint service. It panics without Chains, Endpoints or Sealer.
func NewService(deps Deps) *Service {
	if deps.Chains == nil || deps.Endpoints == nil || deps.Sealer == nil {
		panic("rpc endpoints: chains, endpoints and sealer are required")
	}
	svc := &Service{
		chains:    deps.Chains,
		endpoints: deps.Endpoints,
		sealer:    deps.Sealer,
		verify:    deps.Verify,
		probe:     deps.Probe,
		health:    deps.Health,
		dialer:    deps.Dialer,
		defaults:  deps.Defaults,
		now:       deps.Now,
	}
	if svc.verify == nil {
		svc.verify = chainregistry.VerifyEndpoint
	}
	if svc.probe == nil {
		svc.probe = chainregistry.ProbeEndpoint
	}
	if svc.defaults == nil {
		svc.defaults = DefaultFallbacks
	}
	if svc.now == nil {
		svc.now = time.Now
	}
	return svc
}
