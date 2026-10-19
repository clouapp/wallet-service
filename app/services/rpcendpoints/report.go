package rpcendpoints

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/chainregistry"
)

// Request is one run of chains:rpc-endpoints: a list (Change nil, with Probe to
// ask every endpoint now) or one change, written only with Apply.
type Request struct {
	ChainID string
	Change  *Change
	Probe   bool
	Apply   bool
}

// Report is what the command prints. No line holds a URL: an endpoint is its host.
type Report struct {
	Info    []string
	Warning []string
}

// Run lists or changes the endpoints of one chain and describes the result.
func (s *Service) Run(ctx context.Context, req Request) (Report, error) {
	var report Report
	if req.Change == nil {
		view, err := s.Show(ctx, req.ChainID, req.Probe)
		if err != nil {
			return report, err
		}
		s.describeList(&report, view)
		return report, nil
	}
	plan, err := s.Apply(ctx, req.ChainID, *req.Change, req.Apply)
	describePlan(&report, plan, err == nil)
	if err != nil {
		return report, err
	}
	return report, nil
}

func (s *Service) describeList(report *Report, view View) {
	report.Info = append(report.Info, describeChain(view))
	if !view.FailsOver {
		report.Warning = append(report.Warning, fmt.Sprintf("chain %s (%s) does not fail over through this list; it uses its primary only", view.ChainID, view.AdapterType))
	}
	now := s.now()
	for _, endpoint := range view.Endpoints {
		line := describeEndpoint(endpoint) + "  " + describeHealth(endpoint, now)
		if endpoint.Probe != nil {
			line += "  " + describeProbe(*endpoint.Probe)
		}
		report.Info = append(report.Info, line)
	}
	switch {
	case view.HealthError != "":
		report.Warning = append(report.Warning, view.HealthError)
	case view.HealthObservedAt.IsZero():
		report.Info = append(report.Info, "health: no process recorded a change in the last 24 h (every endpoint answered, or none was called)")
	default:
		report.Info = append(report.Info, fmt.Sprintf("health recorded by the %s process at %s", view.HealthProcess, view.HealthObservedAt.UTC().Format(time.RFC3339)))
	}
}

func describePlan(report *Report, plan Plan, ok bool) {
	if plan.Before.ChainID == "" {
		return
	}
	report.Info = append(report.Info, describeChain(plan.Before))
	for _, endpoint := range plan.Before.Endpoints {
		report.Info = append(report.Info, "  now   "+describeEndpoint(endpoint))
	}
	for _, check := range plan.Checks {
		if check.Err != nil {
			report.Warning = append(report.Warning, fmt.Sprintf("position %d %s: %s", check.Position, check.Host, check.Err))
			continue
		}
		report.Info = append(report.Info, fmt.Sprintf("checked position %d %s: serves %s", check.Position, check.Host, check.Network))
	}
	if !ok {
		return
	}
	if !plan.Changed {
		report.Info = append(report.Info, "nothing to change")
		return
	}
	for _, endpoint := range plan.After.Endpoints {
		report.Info = append(report.Info, "  after "+describeEndpoint(endpoint))
	}
	switch {
	case !plan.Applied:
		report.Info = append(report.Info, "dry run: nothing written (pass --apply)")
	case plan.Reloaded:
		report.Info = append(report.Info, "applied; this process uses the new list, restart the API and workers so theirs load it")
	default:
		report.Info = append(report.Info, "applied; restart the API and workers so the chain adapters load the new list")
	}
}

func describeChain(view View) string {
	network := view.Network
	if network == "" {
		network = "unknown network"
	}
	return fmt.Sprintf("chain %s (%s, %s): %d endpoint(s)", view.ChainID, view.AdapterType, network, len(view.Endpoints))
}

func describeEndpoint(endpoint Endpoint) string {
	role := "fallback"
	if endpoint.Primary {
		role = "primary "
	}
	state := "enabled "
	if !endpoint.Enabled {
		state = "disabled"
	}
	host := endpoint.Host
	if !endpoint.Usable {
		host = "(does not open or resolve here)"
	}
	if endpoint.EnvVar != "" {
		host = "env:" + endpoint.EnvVar + " → " + host
	}
	return fmt.Sprintf("%d %s %s %s", endpoint.Position, role, state, host)
}

func describeHealth(endpoint Endpoint, now time.Time) string {
	health := endpoint.Health
	if health == nil {
		return "health: not recorded"
	}
	var parts []string
	switch {
	case health.WrongNetwork:
		parts = append(parts, "serves another network, not used")
	case health.CoolingDownUntil.After(now):
		parts = append(parts, "skipped until "+health.CoolingDownUntil.UTC().Format(time.RFC3339))
	case health.ConsecutiveFailures > 0:
		parts = append(parts, "failing")
	default:
		parts = append(parts, "healthy")
	}
	if health.ConsecutiveFailures > 0 {
		parts = append(parts, fmt.Sprintf("%d failure(s) in a row", health.ConsecutiveFailures))
	}
	if health.LastError != "" && !health.LastErrorAt.IsZero() {
		parts = append(parts, fmt.Sprintf("last error %s at %s", health.LastError, health.LastErrorAt.UTC().Format(time.RFC3339)))
	}
	return "health: " + strings.Join(parts, ", ")
}

func describeProbe(probe chainregistry.EndpointProbe) string {
	if probe.Err != nil {
		return fmt.Sprintf("probe: failed after %s: %s", probe.Latency.Round(time.Millisecond), probe.Err)
	}
	return fmt.Sprintf("probe: %s, head %d, %s", probe.Network, probe.Head, probe.Latency.Round(time.Millisecond))
}
