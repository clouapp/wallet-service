package rpcendpoints

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

// Action is one kind of change to a chain's endpoint list.
type Action string

const (
	// ActionAdd inserts URL at Position (the end when Position is 0).
	ActionAdd Action = "add"
	// ActionRemove drops the fallback at Position.
	ActionRemove Action = "remove"
	// ActionEnable puts the fallback at Position back in use.
	ActionEnable Action = "enable"
	// ActionDisable keeps the fallback at Position in the list, unused.
	ActionDisable Action = "disable"
	// ActionOrder moves the endpoints: Order lists the current positions in their
	// new order, so its first one becomes the primary.
	ActionOrder Action = "order"
	// ActionSet replaces the whole list with URLs, the primary first.
	ActionSet Action = "set"
	// ActionDefaults appends the default fallbacks of the chain's network that the
	// list does not hold yet (DefaultFallbacks).
	ActionDefaults Action = "defaults"
)

// Change is one change to a chain's endpoint list.
type Change struct {
	Action   Action
	URL      string
	Position int
	Order    []int
	URLs     []string
}

// Check is what an endpoint a change adds said about the network it serves.
// Skipped is an endpoint written under --skip-network-check, never asked.
type Check struct {
	Position int
	Host     string
	Network  string
	Err      error
	Skipped  bool
}

// Plan is a change: the list before and after it, the checks of the endpoints it
// adds, whether it changes anything, whether it was written, and whether the
// adapters of this process already use the new list.
type Plan struct {
	Change   Change
	Before   View
	After    View
	Checks   []Check
	Changed  bool
	Applied  bool
	Reloaded bool
}

// Apply plans change on chainID and, with apply, writes it. Every endpoint the
// change adds or enables, and a new primary, is asked first which network it
// serves: one on another network, or one that does not answer, refuses the whole
// change (a *Refusal) and nothing is written. A chain whose adapter does not fail
// over through this list refuses every change. The primary and the fallbacks are
// written in one transaction.
func (s *Service) Apply(ctx context.Context, chainID string, change Change, apply bool) (Plan, error) {
	return s.apply(ctx, chainID, change, apply, nil, false)
}

// ApplyRecorded is Apply with apply set, running record inside the transaction
// that writes the change, after the write: a record that fails rolls the change
// back. A change that changes nothing calls no record.
func (s *Service) ApplyRecorded(ctx context.Context, chainID string, change Change, record func(ctx context.Context, plan Plan) error) (Plan, error) {
	return s.apply(ctx, chainID, change, true, record, false)
}

func (s *Service) apply(ctx context.Context, chainID string, change Change, apply bool, recordFn func(context.Context, Plan) error, skipNetworkCheck bool) (Plan, error) {
	record, before, err := s.load(ctx, chainID)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Change: change, Before: s.view(record, before)}
	if !models.RPCFallbacksSupported(record.AdapterType) {
		return plan, refuse("chain", "chain %s (%s) does not fail over through this list: its primary is chains:set-rpc%s",
			record.ID, record.AdapterType, ownFailover(record.AdapterType))
	}
	goal := target(record, before)
	after, err := change.on(before, s.defaults(goal.Network))
	if err != nil {
		return plan, err
	}
	if err := validate(after); err != nil {
		return plan, err
	}
	plan.After = s.view(record, after)
	plan.Changed = !sameList(before, after)
	if !plan.Changed {
		return plan, nil
	}
	if after[0].stored == "" || after[0].stored != before[0].stored {
		after[0].verify = true
	}
	plan.Checks = s.check(ctx, record, goal, after, skipNetworkCheck)
	if err := refusedChecks(plan.Checks); err != nil {
		return plan, err
	}
	if !apply {
		return plan, nil
	}
	if err := s.write(ctx, record, before, after, func(ctx context.Context) error {
		if recordFn == nil {
			return nil
		}
		return recordFn(ctx, plan)
	}); err != nil {
		return plan, err
	}
	plan.Applied = true
	plan.Reloaded = s.reload(record.ID, after)
	for _, check := range plan.Checks {
		if check.Skipped {
			slog.Warn("chain rpc endpoint written without a network check", "chain", record.ID, "position", check.Position, "host", check.Host)
		}
	}
	return plan, nil
}

func ownFailover(adapterType string) string {
	if adapterType == models.AdapterTypeBitcoin {
		return "; its fallbacks are <PREFIX>_FALLBACK_RPC_URL (vault.utxo_fallbacks)"
	}
	return ""
}

// on returns the entries after the change; before is not modified.
func (c Change) on(before []entry, defaults []string) ([]entry, error) {
	after := append([]entry(nil), before...)
	switch c.Action {
	case ActionAdd:
		added, err := parseEntry("url", c.URL)
		if err != nil {
			return nil, err
		}
		if at := indexOf(after, added); at >= 0 {
			return nil, refuse("url", "%s is already the endpoint at position %d", added.host(), at+1)
		}
		position := c.Position
		if position == 0 {
			position = len(after) + 1
		}
		if position < 1 || position > len(after)+1 {
			return nil, refuse("position", "position must be between 1 and %d", len(after)+1)
		}
		return insert(after, position-1, added), nil
	case ActionRemove, ActionEnable, ActionDisable:
		if c.Position == models.ChainRPCPrimaryPosition {
			return nil, refuse("position", "position 1 is the primary (chains.rpc_url), always used; move another endpoint to position 1 first (order)")
		}
		if c.Position < 2 || c.Position > len(after) {
			return nil, refuse("position", "position must be a fallback, between 2 and %d", max(len(after), 2))
		}
		i := c.Position - 1
		switch c.Action {
		case ActionRemove:
			return append(after[:i:i], after[i+1:]...), nil
		case ActionEnable:
			if !after[i].enabled {
				after[i].enabled, after[i].verify = true, true
			}
		default:
			after[i].enabled = false
		}
		return after, nil
	case ActionOrder:
		return reorder(after, c.Order)
	case ActionSet:
		if len(c.URLs) == 0 {
			return nil, refuse("urls", "the list needs at least the primary")
		}
		set := make([]entry, 0, len(c.URLs))
		for _, raw := range c.URLs {
			next, err := parseEntry("urls", raw)
			if err != nil {
				return nil, err
			}
			if at := indexOf(set, next); at >= 0 {
				return nil, refuse("urls", "%s is listed twice (positions %d and %d)", next.host(), at+1, len(set)+1)
			}
			if at := indexOf(before, next); at >= 0 {
				kept := before[at]
				kept.verify = !kept.enabled
				kept.enabled = true
				next = kept
			}
			set = append(set, next)
		}
		return set, nil
	case ActionDefaults:
		if len(defaults) == 0 {
			return nil, refuse("defaults", "no default fallbacks are known for this network")
		}
		for _, raw := range defaults {
			next, err := parseEntry("defaults", raw)
			if err != nil {
				return nil, err
			}
			if indexOf(after, next) < 0 {
				after = append(after, next)
			}
		}
		return after, nil
	default:
		return nil, refuse("action", "unknown change %q", c.Action)
	}
}

func reorder(entries []entry, order []int) ([]entry, error) {
	if len(order) != len(entries) {
		return nil, refuse("order", "order must list each of the %d positions once", len(entries))
	}
	seen := make(map[int]bool, len(order))
	moved := make([]entry, 0, len(entries))
	for _, position := range order {
		if position < 1 || position > len(entries) || seen[position] {
			return nil, refuse("order", "order must list each of the %d positions once", len(entries))
		}
		seen[position] = true
		moved = append(moved, entries[position-1])
	}
	return moved, nil
}

// validate refuses a list whose primary is disabled or cannot be dialed here.
func validate(entries []entry) error {
	if len(entries) == 0 {
		return refuse("urls", "the list needs at least the primary")
	}
	if !entries[0].enabled {
		return refuse("order", "a disabled endpoint cannot be the primary; enable it first")
	}
	if !entries[0].usable() {
		return refuse("order", "the primary does not open or resolve here (check APP_KEY and the env variable it names)")
	}
	return nil
}

// check asks every endpoint marked for it which network it serves, in list order;
// with skip it asks none and marks them Skipped.
func (s *Service) check(ctx context.Context, record *models.Chain, goal chainregistry.EndpointTarget, entries []entry, skip bool) []Check {
	var checks []Check
	for i, e := range entries {
		if !e.verify || !e.enabled {
			continue
		}
		if skip {
			checks = append(checks, Check{Position: i + 1, Host: e.host(), Skipped: true})
			continue
		}
		network, err := s.verify(ctx, record.AdapterType, goal, e.dial)
		checks = append(checks, Check{Position: i + 1, Host: e.host(), Network: network, Err: err})
	}
	return checks
}

func refusedChecks(checks []Check) error {
	var reasons []string
	for _, check := range checks {
		if check.Err == nil {
			continue
		}
		reason := "did not say which network it serves"
		if errors.Is(check.Err, chain.ErrWrongNetwork) {
			reason = "serves another network"
		}
		reasons = append(reasons, fmt.Sprintf("position %d %s %s (%s)", check.Position, check.Host, reason, check.Err))
	}
	if len(reasons) == 0 {
		return nil
	}
	return refuse("url", "refused, nothing written: %s", strings.Join(reasons, "; "))
}

// write stores the primary (only when it changed) and the fallbacks, then runs
// recorded, in one transaction. Every fallback is sealed again: a former primary
// may hold the older envelope, which chain_rpc_endpoints does not accept.
func (s *Service) write(ctx context.Context, record *models.Chain, before, after []entry, recorded func(context.Context) error) error {
	primary := ""
	if after[0].stored == "" || after[0].stored != before[0].stored {
		sealed, err := s.seal(after[0].plain)
		if err != nil {
			return err
		}
		primary = sealed
	}
	rows := make([]models.ChainRPCEndpoint, 0, len(after)-1)
	for i, e := range after[1:] {
		sealed := e.stored
		if e.plain != "" {
			resealed, err := s.seal(e.plain)
			if err != nil {
				return err
			}
			sealed = resealed
		}
		rows = append(rows, models.ChainRPCEndpoint{
			URL: sealed, Position: i + models.ChainRPCPrimaryPosition + 1, Enabled: e.enabled, CreatedAt: e.createdAt,
		})
	}
	return s.endpoints.Within(ctx, func(ctx context.Context) error {
		if primary != "" {
			if err := s.chains.UpdateRPCURL(ctx, record.ID, primary); err != nil {
				return fmt.Errorf("write the primary of %s: %w", record.ID, err)
			}
		}
		if err := s.endpoints.Replace(ctx, record.ID, rows); err != nil {
			return fmt.Errorf("write the fallbacks of %s: %w", record.ID, err)
		}
		return recorded(ctx)
	})
}

func (s *Service) seal(plain string) (string, error) {
	sealed, err := s.sealer.Seal(plain)
	if err != nil || strings.TrimSpace(sealed) == "" || sealed == plain {
		return "", fmt.Errorf("seal rpc endpoint")
	}
	return sealed, nil
}

// reload points this process's adapter at the new list; it reports whether it did.
func (s *Service) reload(chainID string, entries []entry) bool {
	if s.dialer == nil {
		return false
	}
	var fallbacks []chain.RPCFallback
	for i, e := range entries[1:] {
		if e.enabled && e.usable() {
			fallbacks = append(fallbacks, chain.RPCFallback{URL: e.dial, Position: i + models.ChainRPCPrimaryPosition + 1})
		}
	}
	reloaded, err := s.dialer.ReplaceEndpoints(chainID, entries[0].dial, fallbacks)
	return err == nil && reloaded
}

// parseEntry reads an http(s) URL or an env:NAME reference that resolves here.
func parseEntry(field, raw string) (entry, error) {
	plain := strings.TrimSpace(raw)
	switch {
	case plain == "":
		return entry{}, refuse(field, "an endpoint URL is required")
	case strings.HasPrefix(plain, models.RPCURLEnvPrefix):
		if !models.IsValidRPCURLEnvReference(plain) {
			return entry{}, refuse(field, "an env reference must look like env:ETH_RPC_URL")
		}
	case !httpURL(plain):
		return entry{}, refuse(field, "an endpoint must be an http or https URL, or env:NAME")
	}
	dial, err := models.DialEndpoint(plain)
	if err != nil {
		return entry{}, refuse(field, "the endpoint does not resolve here (an env:NAME must name a variable set to an http or https URL)")
	}
	return entry{plain: plain, dial: dial, enabled: true, verify: true}, nil
}

func httpURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (e entry) host() string {
	if host := chain.RPCEndpointHost(e.dial); host != "" {
		return host
	}
	return "(unusable endpoint)"
}

// indexOf is the index of the entry that dials the same URL (or names the same
// reference) as target, or -1.
func indexOf(entries []entry, target entry) int {
	for i, e := range entries {
		if (e.dial != "" && e.dial == target.dial) || (e.plain != "" && e.plain == target.plain) {
			return i
		}
	}
	return -1
}

func insert(entries []entry, at int, added entry) []entry {
	out := make([]entry, 0, len(entries)+1)
	out = append(out, entries[:at]...)
	out = append(out, added)
	return append(out, entries[at:]...)
}

// sameList reports whether two lists dial the same endpoints, in the same order,
// with the same flags.
func sameList(a, b []entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].plain != b[i].plain || a[i].stored != b[i].stored || a[i].enabled != b[i].enabled {
			return false
		}
	}
	return true
}
