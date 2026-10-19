package chains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

// Body fields of the platform endpoint-list routes.
const (
	fieldEndpointURL      = "url"
	fieldEndpointURLs     = "urls"
	fieldEndpointPosition = "position"
	fieldEndpointEnabled  = "enabled"
	fieldEndpointOrder    = "order"

	// endpointListField is the field name the activity row records for any change
	// of the list; the URLs are never recorded.
	endpointListField = "rpc_endpoints"
)

// EndpointList is the endpoint-list service the platform routes call
// (*rpcendpoints.Service).
type EndpointList interface {
	Show(ctx context.Context, chainID string, probe bool) (rpcendpoints.View, error)
	ApplyRecorded(ctx context.Context, chainID string, change rpcendpoints.Change, record func(context.Context, rpcendpoints.Plan) error) (rpcendpoints.Plan, error)
}

// RPCEndpoints is the platform editor of a chain's endpoint list. Like the
// rpc_url editor, a platform_admins row is the gate (chains.view, chains.update),
// an unknown chain is ErrNotFound before the admin check, and no URL is returned
// or recorded: an endpoint is its host and fingerprint.
type RPCEndpoints struct {
	list     EndpointList
	admins   PlatformAdmins
	activity activitylog.Writer
}

// RPCEndpointsDeps is everything the platform endpoint-list editor needs. List,
// Admins and Activity are required.
type RPCEndpointsDeps struct {
	List     EndpointList
	Admins   PlatformAdmins
	Activity activitylog.Writer
}

// NewRPCEndpoints builds the platform endpoint-list editor.
func NewRPCEndpoints(deps RPCEndpointsDeps) *RPCEndpoints {
	if deps.List == nil {
		panic("chain rpc endpoints: endpoint list is required")
	}
	if deps.Admins == nil {
		panic("chain rpc endpoints: platform admins are required")
	}
	if deps.Activity == nil {
		panic("chain rpc endpoints: activity log is required")
	}
	return &RPCEndpoints{list: deps.List, admins: deps.Admins, activity: deps.Activity}
}

// List returns the chain's endpoints with their recorded health and, with probe,
// what each endpoint answers now.
func (s *RPCEndpoints) List(ctx context.Context, actorID uuid.UUID, chainID string, probe bool) (rpcendpoints.View, error) {
	view, err := s.list.Show(ctx, chainID, false)
	if err != nil {
		return rpcendpoints.View{}, endpointError(err)
	}
	if err = s.requireAdmin(ctx, actorID); err != nil {
		return rpcendpoints.View{}, err
	}
	if !probe {
		return view, nil
	}
	view, err = s.list.Show(ctx, chainID, true)
	return view, endpointError(err)
}

// Add inserts {"url", "position"?}: at the end without a position, 1 makes it the primary.
func (s *RPCEndpoints) Add(ctx context.Context, actorID uuid.UUID, chainID string, body map[string]json.RawMessage) (rpcendpoints.Plan, error) {
	invalid := onlyFields(body, fieldEndpointURL, fieldEndpointPosition)
	url := requiredString(body, fieldEndpointURL, invalid)
	position := optionalPosition(body, fieldEndpointPosition, invalid)
	return s.change(ctx, actorID, chainID, invalid, rpcendpoints.Change{Action: rpcendpoints.ActionAdd, URL: url, Position: position})
}

// Replace sets the whole list from {"urls": [...]}, the primary first.
func (s *RPCEndpoints) Replace(ctx context.Context, actorID uuid.UUID, chainID string, body map[string]json.RawMessage) (rpcendpoints.Plan, error) {
	invalid := onlyFields(body, fieldEndpointURLs)
	var urls []string
	if decodeRequired(body, fieldEndpointURLs, &urls, "must be a list of URLs", invalid) && len(urls) == 0 {
		invalid.add(fieldEndpointURLs, "must hold at least the primary")
	}
	return s.change(ctx, actorID, chainID, invalid, rpcendpoints.Change{Action: rpcendpoints.ActionSet, URLs: urls})
}

// Reorder moves the endpoints from {"order": [current positions in the new order]}.
func (s *RPCEndpoints) Reorder(ctx context.Context, actorID uuid.UUID, chainID string, body map[string]json.RawMessage) (rpcendpoints.Plan, error) {
	invalid := onlyFields(body, fieldEndpointOrder)
	var order []int
	decodeRequired(body, fieldEndpointOrder, &order, "must be a list of positions", invalid)
	return s.change(ctx, actorID, chainID, invalid, rpcendpoints.Change{Action: rpcendpoints.ActionOrder, Order: order})
}

// SetEnabled enables or disables the fallback at position from {"enabled": bool}.
func (s *RPCEndpoints) SetEnabled(ctx context.Context, actorID uuid.UUID, chainID string, position int, body map[string]json.RawMessage) (rpcendpoints.Plan, error) {
	invalid := onlyFields(body, fieldEndpointEnabled)
	var enabled bool
	decodeRequired(body, fieldEndpointEnabled, &enabled, "must be true or false", invalid)
	action := rpcendpoints.ActionDisable
	if enabled {
		action = rpcendpoints.ActionEnable
	}
	return s.change(ctx, actorID, chainID, invalid, rpcendpoints.Change{Action: action, Position: position})
}

// Remove drops the fallback at position.
func (s *RPCEndpoints) Remove(ctx context.Context, actorID uuid.UUID, chainID string, position int) (rpcendpoints.Plan, error) {
	return s.change(ctx, actorID, chainID, &ValidationError{}, rpcendpoints.Change{Action: rpcendpoints.ActionRemove, Position: position})
}

// AddDefaults appends the default fallbacks of the chain's network it does not hold.
func (s *RPCEndpoints) AddDefaults(ctx context.Context, actorID uuid.UUID, chainID string) (rpcendpoints.Plan, error) {
	return s.change(ctx, actorID, chainID, &ValidationError{}, rpcendpoints.Change{Action: rpcendpoints.ActionDefaults})
}

// change runs one change: the chain must exist (ErrNotFound), the caller must be a
// platform admin (ErrPlatformForbidden), the body must be valid and the change
// accepted (ValidationError); then it is written and recorded.
func (s *RPCEndpoints) change(ctx context.Context, actorID uuid.UUID, chainID string, invalid *ValidationError, change rpcendpoints.Change) (rpcendpoints.Plan, error) {
	if _, err := s.list.Show(ctx, chainID, false); err != nil {
		return rpcendpoints.Plan{}, endpointError(err)
	}
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return rpcendpoints.Plan{}, err
	}
	if !invalid.empty() {
		return rpcendpoints.Plan{}, invalid
	}
	plan, err := s.list.ApplyRecorded(ctx, chainID, change, func(ctx context.Context, plan rpcendpoints.Plan) error {
		meta, err := activitylog.ChainThresholdsChange(plan.Before.ChainID, []string{endpointListField})
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: actorID,
			Action:      activitylog.ActionChainsUpdated,
			TargetType:  activitylog.TargetChain,
			TargetID:    plan.Before.ChainID,
			Metadata:    meta,
		})
	})
	return plan, endpointError(err)
}

func (s *RPCEndpoints) requireAdmin(ctx context.Context, actorID uuid.UUID) error {
	if actorID == uuid.Nil {
		return fmt.Errorf("chain rpc endpoints: actor is required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return ErrPlatformForbidden
	}
	return nil
}

// endpointError maps the endpoint service's answers onto this package's: an
// unknown chain is ErrNotFound and a refused change a ValidationError on the field
// it names.
func endpointError(err error) error {
	var refusal *rpcendpoints.Refusal
	switch {
	case err == nil:
		return nil
	case errors.Is(err, rpcendpoints.ErrChainNotFound):
		return ErrNotFound
	case errors.As(err, &refusal):
		invalid := &ValidationError{}
		invalid.add(refusal.Field, refusal.Reason)
		return invalid
	default:
		return err
	}
}

func onlyFields(body map[string]json.RawMessage, allowed ...string) *ValidationError {
	invalid := &ValidationError{}
	for key := range body {
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			invalid.add(key, "unknown field")
		}
	}
	return invalid
}

// decodeRequired decodes field into out and reports whether it did.
func decodeRequired(body map[string]json.RawMessage, field string, out any, typeMessage string, invalid *ValidationError) bool {
	raw, ok := body[field]
	if !ok {
		invalid.add(field, "is required")
		return false
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		invalid.add(field, "must not be null")
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		invalid.add(field, typeMessage)
		return false
	}
	return true
}

func requiredString(body map[string]json.RawMessage, field string, invalid *ValidationError) string {
	var value string
	if decodeRequired(body, field, &value, "must be a string", invalid) && strings.TrimSpace(value) == "" {
		invalid.add(field, "must not be empty")
	}
	return strings.TrimSpace(value)
}

func optionalPosition(body map[string]json.RawMessage, field string, invalid *ValidationError) int {
	if _, ok := body[field]; !ok {
		return 0
	}
	var position int
	if decodeRequired(body, field, &position, "must be an integer", invalid) && position < 1 {
		invalid.add(field, "must be 1 or more")
	}
	return position
}
