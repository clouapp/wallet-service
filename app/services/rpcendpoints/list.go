package rpcendpoints

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

// Endpoint is one entry of a chain's list as an operator sees it, never with its
// URL: Host is the host[:port] of the URL it dials, EnvVar the variable an
// env:NAME entry names. Usable is false for a stored value that does not open or
// resolve in this process. Health is what a running process last recorded for this
// URL (nil when none did); Probe is what the endpoint answered just now, when asked.
type Endpoint struct {
	Position    int
	Primary     bool
	Host        string
	EnvVar      string
	Fingerprint string
	Enabled     bool
	Usable      bool
	Health      *chain.RPCEndpointHealth
	Probe       *chainregistry.EndpointProbe
}

// View is the endpoint list of one chain, the primary first. FailsOver is false
// for a chain whose adapter does not use the fallbacks (Bitcoin family, TRON, XRP).
// HealthObservedAt and HealthProcess say when and where the health was recorded;
// HealthError says why it could not be read.
type View struct {
	ChainID          string
	AdapterType      string
	Network          string
	FailsOver        bool
	Endpoints        []Endpoint
	HealthObservedAt time.Time
	HealthProcess    string
	HealthError      string
}

// entry is one endpoint while a list is read or changed.
type entry struct {
	stored    string // as stored (sealed); empty for an endpoint this change adds
	plain     string // opened: a URL or env:NAME
	dial      string // the URL called; empty when plain does not resolve here
	enabled   bool
	createdAt time.Time
	verify    bool // asked which network it serves before the change is saved
}

func (e entry) usable() bool { return e.dial != "" }

// Show returns the endpoint list of chainID with the health the running processes
// recorded and, with probe, what each endpoint answers now.
func (s *Service) Show(ctx context.Context, chainID string, probe bool) (View, error) {
	record, entries, err := s.load(ctx, chainID)
	if err != nil {
		return View{}, err
	}
	view := s.view(record, entries)
	s.attachHealth(ctx, &view)
	if probe {
		s.attachProbes(ctx, record, entries, &view)
	}
	return view, nil
}

// load reads the chain and its entries: the primary, then the fallbacks in order.
func (s *Service) load(ctx context.Context, chainID string) (*models.Chain, []entry, error) {
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return nil, nil, ErrChainNotFound
	}
	record, err := s.chains.FindByID(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, nil, ErrChainNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load chain %s: %w", chainID, err)
	}
	if record == nil || record.ID == "" {
		return nil, nil, ErrChainNotFound
	}
	rows, err := s.endpoints.ListByChain(ctx, record.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load the rpc endpoints of %s: %w", record.ID, err)
	}
	entries := make([]entry, 0, len(rows)+1)
	entries = append(entries, s.open(record.RpcURL, true, time.Time{}))
	for _, row := range rows {
		entries = append(entries, s.open(row.URL, row.Enabled, row.CreatedAt))
	}
	return record, entries, nil
}

func (s *Service) open(stored string, enabled bool, createdAt time.Time) entry {
	opened := entry{stored: stored, enabled: enabled, createdAt: createdAt}
	plain, err := s.sealer.Open(stored)
	if err != nil {
		return opened
	}
	opened.plain = strings.TrimSpace(plain)
	if dial, err := models.DialEndpoint(opened.plain); err == nil {
		opened.dial = dial
	}
	return opened
}

// target is the network every endpoint of record must serve.
func target(record *models.Chain, entries []entry) chainregistry.EndpointTarget {
	primary := ""
	if len(entries) > 0 {
		primary = entries[0].dial
	}
	return chainregistry.ChainEndpointTarget(*record, primary)
}

func (s *Service) view(record *models.Chain, entries []entry) View {
	view := View{
		ChainID:     record.ID,
		AdapterType: record.AdapterType,
		Network:     target(record, entries).Network,
		FailsOver:   models.RPCFallbacksSupported(record.AdapterType),
		Endpoints:   make([]Endpoint, 0, len(entries)),
	}
	for i, e := range entries {
		endpoint := Endpoint{Position: i + 1, Primary: i == 0, Enabled: e.enabled, Usable: e.usable()}
		if e.usable() {
			endpoint.Host = chain.RPCEndpointHost(e.dial)
			endpoint.Fingerprint = chain.RPCEndpointFingerprint(e.dial)
		}
		if strings.HasPrefix(e.plain, models.RPCURLEnvPrefix) {
			endpoint.EnvVar = strings.TrimPrefix(e.plain, models.RPCURLEnvPrefix)
		}
		view.Endpoints = append(view.Endpoints, endpoint)
	}
	return view
}

// attachHealth adds the recorded health of each endpoint, matched by fingerprint
// (the URL, not its position: a list changed since keeps the health of an
// endpoint that stayed).
func (s *Service) attachHealth(ctx context.Context, view *View) {
	if s.health == nil {
		return
	}
	snapshot, found, err := s.health.RPCHealth(ctx, view.ChainID)
	if err != nil {
		view.HealthError = "health not read: " + err.Error()
		return
	}
	if !found {
		return
	}
	view.HealthObservedAt, view.HealthProcess = snapshot.ObservedAt, snapshot.Process
	byFingerprint := make(map[string]chain.RPCEndpointHealth, len(snapshot.Endpoints))
	for _, health := range snapshot.Endpoints {
		byFingerprint[health.Fingerprint] = health
	}
	for i := range view.Endpoints {
		if health, ok := byFingerprint[view.Endpoints[i].Fingerprint]; ok && view.Endpoints[i].Fingerprint != "" {
			recorded := health
			view.Endpoints[i].Health = &recorded
		}
	}
}

// attachProbes asks every usable endpoint, at once and under checkBudget, its
// network and head.
func (s *Service) attachProbes(ctx context.Context, record *models.Chain, entries []entry, view *View) {
	ctx, cancel := context.WithTimeout(ctx, checkBudget)
	defer cancel()
	goal := target(record, entries)
	var wg sync.WaitGroup
	for i, e := range entries {
		if !e.usable() {
			continue
		}
		wg.Add(1)
		go func(i int, dial string) {
			defer wg.Done()
			probe := s.probe(ctx, record.AdapterType, goal, dial)
			view.Endpoints[i].Probe = &probe
		}(i, e.dial)
	}
	wg.Wait()
}
