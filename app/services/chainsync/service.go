// Package chainsync keeps a running process's chain adapters on what the database
// holds now: an RPC endpoint list changed by another process (chains:rpc-endpoints,
// chains:set-rpc, the platform API served by another process) and the TronGrid key of
// the provider_tron setting reach every adapter within one interval, without a
// restart. A chain added or removed still needs a registry reload (a restart, or a
// platform chain edit in that process).
package chainsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// DefaultInterval is how often a pass reads the endpoint lists and the TronGrid key.
const DefaultInterval = 30 * time.Second

// EndpointList is one chain's opened endpoints: its primary and its enabled
// fallbacks with their stored positions. URLs are never logged.
type EndpointList struct {
	Primary   string
	Fallbacks []chain.RPCFallback
}

// EndpointLister reads every active chain's endpoint list from the database.
type EndpointLister func(ctx context.Context) (map[string]EndpointList, error)

// Registry is the part of the chain registry a pass updates.
type Registry interface {
	ChainIDs() []string
	Chain(id string) (types.Chain, error)
	ReplaceEndpoint(chainID, endpoint string) (bool, error)
	ReplaceEndpoints(chainID, primary string, fallbacks []chain.RPCFallback) (bool, error)
}

// apiKeySetter is an adapter whose provider key can change at runtime (TRON).
type apiKeySetter interface {
	SetAPIKey(key string)
}

// Deps is what a sync needs. Registry and Endpoints are required; TronAPIKey is
// optional (nil: the key the registry was built with stays).
type Deps struct {
	Registry   Registry
	Endpoints  EndpointLister
	TronAPIKey func(ctx context.Context) string
	Interval   time.Duration
}

// Service runs the passes. It is used by one goroutine (Run).
type Service struct {
	registry  Registry
	endpoints EndpointLister
	tronKey   func(ctx context.Context) string
	interval  time.Duration

	applied   map[string]string
	keyDigest string
	baselined bool
}

// New builds a sync.
func New(deps Deps) *Service {
	if deps.Registry == nil {
		panic("chain sync: registry is required")
	}
	if deps.Endpoints == nil {
		panic("chain sync: endpoint lister is required")
	}
	interval := deps.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Service{registry: deps.Registry, endpoints: deps.Endpoints, tronKey: deps.TronAPIKey, interval: interval, applied: map[string]string{}}
}

// Run records what the adapters were built with, then applies each change it reads,
// every interval, until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.Pass(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.Pass(ctx)
	}
}

// Pass reads the endpoint lists and the TronGrid key once. The first pass only
// records them (the registry was just built from the same rows); a later one replaces
// the list of each chain whose list changed and sets a changed key on every adapter
// that takes one. It returns how many chains it moved.
func (s *Service) Pass(ctx context.Context) int {
	moved := 0
	lists, err := s.endpoints(ctx)
	if err != nil {
		slog.Warn("chain sync: endpoint lists unread; the adapters keep theirs", "error", err)
	} else {
		moved = s.applyEndpoints(lists)
	}
	s.applyTronKey(ctx)
	s.baselined = true
	return moved
}

func (s *Service) applyEndpoints(lists map[string]EndpointList) int {
	moved := 0
	for _, chainID := range s.registry.ChainIDs() {
		list, ok := lists[chainID]
		if !ok || strings.TrimSpace(list.Primary) == "" {
			continue
		}
		digest := listDigest(list)
		if s.applied[chainID] == digest {
			continue
		}
		previous, seen := s.applied[chainID]
		s.applied[chainID] = digest
		if !s.baselined || !seen || previous == "" {
			continue
		}
		replaced, err := s.registry.ReplaceEndpoints(chainID, list.Primary, list.Fallbacks)
		if err != nil {
			// An adapter that keeps its own failover (the Bitcoin family) takes its
			// primary only.
			replaced, err = s.registry.ReplaceEndpoint(chainID, list.Primary)
		}
		if err != nil || !replaced {
			slog.Warn("chain sync: endpoint list changed but the adapter was not moved", "chain", chainID)
			delete(s.applied, chainID)
			continue
		}
		moved++
		slog.Info("chain sync: rpc endpoint list reloaded from the database", "chain", chainID, "fallbacks", len(list.Fallbacks))
	}
	return moved
}

func (s *Service) applyTronKey(ctx context.Context) {
	if s.tronKey == nil {
		return
	}
	key := s.tronKey(ctx)
	digest := digestOf(key)
	if digest == s.keyDigest {
		return
	}
	first := !s.baselined
	s.keyDigest = digest
	if first {
		return
	}
	set := 0
	for _, chainID := range s.registry.ChainIDs() {
		adapter, err := s.registry.Chain(chainID)
		if err != nil {
			continue
		}
		if setter, ok := adapter.(apiKeySetter); ok {
			setter.SetAPIKey(key)
			set++
		}
	}
	slog.Info("chain sync: provider key replaced", "provider", "trongrid", "chains", set, "key_set", key != "")
}

func listDigest(list EndpointList) string {
	var b strings.Builder
	b.WriteString(list.Primary)
	for _, fallback := range list.Fallbacks {
		b.WriteString("\x00")
		b.WriteString(strconv.Itoa(fallback.Position))
		b.WriteString("\x00")
		b.WriteString(fallback.URL)
	}
	return digestOf(b.String())
}

func digestOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
