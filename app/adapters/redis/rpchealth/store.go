// Package rpchealth keeps, in Redis, the last health each process recorded for the
// RPC endpoints of a chain, so a command or the platform API can show it.
package rpchealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	// keyPrefix + chain id holds the chain's last snapshot (one JSON value).
	keyPrefix = "vault:rpc-health:"
	// snapshotTTL drops the snapshot of a chain no process has called for a day.
	snapshotTTL = 24 * time.Hour
	// writeTimeout bounds one background write.
	writeTimeout = 2 * time.Second
	// queueSize is how many snapshots wait for the writer; more are dropped (the
	// next change writes again).
	queueSize = 64
)

// Store records RPC endpoint health (chain.RPCHealthSink) and reads it back. A
// write never blocks the RPC call that reports it: one background writer keeps the
// order of the snapshots. Snapshots hold hosts and fingerprints, never a URL.
type Store struct {
	client  redis.UniversalClient
	process string
	now     func() time.Time

	start sync.Once
	queue chan write
}

type write struct {
	key     string
	payload []byte
}

// record is the stored JSON of one snapshot.
type record struct {
	ObservedAt time.Time        `json:"observed_at"`
	Process    string           `json:"process"`
	Endpoints  []endpointRecord `json:"endpoints"`
}

type endpointRecord struct {
	Position            int       `json:"position"`
	Host                string    `json:"host"`
	Fingerprint         string    `json:"fingerprint"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CoolingDownUntil    time.Time `json:"cooling_down_until"`
	LastError           string    `json:"last_error,omitempty"`
	LastErrorAt         time.Time `json:"last_error_at"`
	WrongNetwork        bool      `json:"wrong_network,omitempty"`
}

var _ chain.RPCHealthSink = (*Store)(nil)

// New wraps client. process names the recording process in each snapshot. A nil
// client returns a nil store, which records and reads nothing.
func New(client redis.UniversalClient, process string) *Store {
	if client == nil {
		return nil
	}
	process = strings.TrimSpace(process)
	if process == "" {
		process = "local"
	}
	return &Store{client: client, process: process, now: time.Now, queue: make(chan write, queueSize)}
}

// RecordRPCHealth queues the chain's snapshot for the background writer. A full
// queue drops it.
func (s *Store) RecordRPCHealth(chainID string, endpoints []chain.RPCEndpointHealth) {
	chainID = strings.TrimSpace(chainID)
	if s == nil || s.client == nil || chainID == "" {
		return
	}
	payload, err := json.Marshal(toRecord(s.now().UTC(), s.process, endpoints))
	if err != nil {
		return
	}
	s.start.Do(func() { go s.run() })
	select {
	case s.queue <- write{key: keyPrefix + chainID, payload: payload}:
	default:
	}
}

func (s *Store) run() {
	for next := range s.queue {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		if err := s.client.Set(ctx, next.key, next.payload, snapshotTTL).Err(); err != nil {
			slog.Debug("rpc health not recorded", "key", next.key, "error", err)
		}
		cancel()
	}
}

// RPCHealth reads the last snapshot of chainID. found is false when no process
// recorded one in the last day.
func (s *Store) RPCHealth(ctx context.Context, chainID string) (chain.RPCHealthSnapshot, bool, error) {
	if s == nil || s.client == nil {
		return chain.RPCHealthSnapshot{}, false, nil
	}
	raw, err := s.client.Get(ctx, keyPrefix+strings.TrimSpace(chainID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return chain.RPCHealthSnapshot{}, false, nil
	}
	if err != nil {
		return chain.RPCHealthSnapshot{}, false, fmt.Errorf("read rpc health of %s: %w", chainID, err)
	}
	var stored record
	if err := json.Unmarshal(raw, &stored); err != nil {
		return chain.RPCHealthSnapshot{}, false, fmt.Errorf("decode rpc health of %s: %w", chainID, err)
	}
	return fromRecord(stored), true, nil
}

func toRecord(observedAt time.Time, process string, endpoints []chain.RPCEndpointHealth) record {
	out := record{ObservedAt: observedAt, Process: process, Endpoints: make([]endpointRecord, 0, len(endpoints))}
	for _, e := range endpoints {
		out.Endpoints = append(out.Endpoints, endpointRecord{
			Position: e.Position, Host: e.Host, Fingerprint: e.Fingerprint,
			ConsecutiveFailures: e.ConsecutiveFailures, CoolingDownUntil: e.CoolingDownUntil.UTC(),
			LastError: e.LastError, LastErrorAt: e.LastErrorAt.UTC(), WrongNetwork: e.WrongNetwork,
		})
	}
	return out
}

func fromRecord(stored record) chain.RPCHealthSnapshot {
	out := chain.RPCHealthSnapshot{ObservedAt: stored.ObservedAt, Process: stored.Process}
	for _, e := range stored.Endpoints {
		out.Endpoints = append(out.Endpoints, chain.RPCEndpointHealth{
			Position: e.Position, Host: e.Host, Fingerprint: e.Fingerprint,
			ConsecutiveFailures: e.ConsecutiveFailures, CoolingDownUntil: e.CoolingDownUntil,
			LastError: e.LastError, LastErrorAt: e.LastErrorAt, WrongNetwork: e.WrongNetwork,
		})
	}
	return out
}
