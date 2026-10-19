// Package chains shapes the answers of the platform chain routes that are not a
// service view.
package chains

import (
	"time"

	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

// RPCEndpointList is a chain's endpoint list, the primary first. No URL is ever
// part of it: an endpoint is its host and a fingerprint of its URL.
type RPCEndpointList struct {
	ChainID          string        `json:"chainId" example:"eth"`
	AdapterType      string        `json:"adapterType" example:"evm"`
	Network          string        `json:"network" example:"ethereum-sepolia"`
	FailsOver        bool          `json:"failsOver" example:"true"`
	Endpoints        []RPCEndpoint `json:"endpoints"`
	HealthObservedAt *time.Time    `json:"healthObservedAt"`
	HealthProcess    string        `json:"healthProcess" example:"deposit_scanner"`
}

// RPCEndpoint is one endpoint of the list. Health is what a running process last
// recorded for it (null when none did); Probe is what it answered when asked now
// (only with ?probe=true).
type RPCEndpoint struct {
	Position    int                `json:"position" example:"1"`
	Primary     bool               `json:"primary" example:"true"`
	Host        string             `json:"host" example:"sepolia.drpc.org"`
	EnvVar      string             `json:"envVar" example:""`
	Fingerprint string             `json:"fingerprint" example:"3f1c0a9b7e2d"`
	Enabled     bool               `json:"enabled" example:"true"`
	Usable      bool               `json:"usable" example:"true"`
	Health      *RPCEndpointHealth `json:"health"`
	Probe       *RPCEndpointProbe  `json:"probe,omitempty"`
}

// RPCEndpointHealth is the recorded health of one endpoint.
type RPCEndpointHealth struct {
	ConsecutiveFailures int        `json:"consecutiveFailures" example:"0"`
	CoolingDownUntil    *time.Time `json:"coolingDownUntil"`
	LastError           string     `json:"lastError" example:"timeout"`
	LastErrorAt         *time.Time `json:"lastErrorAt"`
	WrongNetwork        bool       `json:"wrongNetwork" example:"false"`
}

// RPCEndpointProbe is what an endpoint answered when asked now.
type RPCEndpointProbe struct {
	Network   string `json:"network" example:"ethereum-sepolia"`
	Head      uint64 `json:"head" example:"11937030"`
	LatencyMs int64  `json:"latencyMs" example:"120"`
	Error     string `json:"error,omitempty"`
}

// RPCEndpointChange is the list after a change. changed is false when the list
// already was so (nothing written); reloaded is true when the adapters of the
// process that answered already use it (the others load it on restart).
type RPCEndpointChange struct {
	RPCEndpointList
	Changed  bool `json:"changed" example:"true"`
	Reloaded bool `json:"reloaded" example:"true"`
}

// ListFrom shapes a list view.
func ListFrom(view rpcendpoints.View) RPCEndpointList {
	list := RPCEndpointList{
		ChainID:          view.ChainID,
		AdapterType:      view.AdapterType,
		Network:          view.Network,
		FailsOver:        view.FailsOver,
		Endpoints:        make([]RPCEndpoint, 0, len(view.Endpoints)),
		HealthObservedAt: optionalTime(view.HealthObservedAt),
		HealthProcess:    view.HealthProcess,
	}
	for _, endpoint := range view.Endpoints {
		item := RPCEndpoint{
			Position: endpoint.Position, Primary: endpoint.Primary, Host: endpoint.Host, EnvVar: endpoint.EnvVar,
			Fingerprint: endpoint.Fingerprint, Enabled: endpoint.Enabled, Usable: endpoint.Usable,
		}
		if health := endpoint.Health; health != nil {
			item.Health = &RPCEndpointHealth{
				ConsecutiveFailures: health.ConsecutiveFailures,
				CoolingDownUntil:    optionalTime(health.CoolingDownUntil),
				LastError:           health.LastError,
				LastErrorAt:         optionalTime(health.LastErrorAt),
				WrongNetwork:        health.WrongNetwork,
			}
		}
		if probe := endpoint.Probe; probe != nil {
			item.Probe = &RPCEndpointProbe{Network: probe.Network, Head: probe.Head, LatencyMs: probe.Latency.Milliseconds()}
			if probe.Err != nil {
				item.Probe.Error = probe.Err.Error()
			}
		}
		list.Endpoints = append(list.Endpoints, item)
	}
	return list
}

// ChangeFrom shapes the answer of a change: the list after it.
func ChangeFrom(plan rpcendpoints.Plan) RPCEndpointChange {
	after := plan.After
	if !plan.Changed {
		after = plan.Before
	}
	return RPCEndpointChange{RPCEndpointList: ListFrom(after), Changed: plan.Changed, Reloaded: plan.Reloaded}
}

func optionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}
