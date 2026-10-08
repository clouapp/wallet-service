package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

const fieldRPCURL = "rpcUrl"

// RPCStore reads one chain and writes its sealed rpc_url.
type RPCStore interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
	UpdateRPCURL(ctx context.Context, id, sealed string) error
}

// RPCSealer seals a URL for chains.rpc_url and opens it again. The plaintext
// is never written into an error.
type RPCSealer interface {
	Seal(plaintext string) (string, error)
	Open(stored string) (string, error)
}

// EndpointDialer points the loaded chain dialer at an opened endpoint.
// A chain that is not loaded returns false. The endpoint is not logged.
type EndpointDialer interface {
	ReplaceEndpoint(chainID, endpoint string) (bool, error)
}

// RPCView is the write-only answer S1.4.4 names. The URL is not included.
type RPCView struct {
	RPCURLSet bool `json:"rpcUrlSet"`
}

// RPC edits chains.rpc_url. S1.4.7 names chains.view and chains.update on
// ChainThresholdCatalog, the entry this route shares with the threshold
// PATCH. The URL is write-only and is never returned. There is no platform
// permission catalog, so a platform_admins row is the gate. The pair is not
// a second gate.
type RPC struct {
	store    RPCStore
	admins   PlatformAdmins
	activity activitylog.Writer
	sealer   RPCSealer
	dialer   EndpointDialer
}

// RPCDeps is everything the platform chain-RPC editor needs. Store, Admins,
// Activity, Sealer and Dialer are required.
type RPCDeps struct {
	Store    RPCStore
	Admins   PlatformAdmins
	Activity activitylog.Writer
	Sealer   RPCSealer
	Dialer   EndpointDialer
}

// NewRPC builds the platform chain-RPC editor.
func NewRPC(deps RPCDeps) *RPC {
	if deps.Store == nil {
		panic("chain rpc: store is required")
	}
	if deps.Admins == nil {
		panic("chain rpc: platform admins are required")
	}
	if deps.Activity == nil {
		panic("chain rpc: activity log is required")
	}
	if deps.Sealer == nil {
		panic("chain rpc: sealer is required")
	}
	if deps.Dialer == nil {
		panic("chain rpc: dialer is required")
	}
	return &RPC{
		store:    deps.Store,
		admins:   deps.Admins,
		activity: deps.Activity,
		sealer:   deps.Sealer,
		dialer:   deps.Dialer,
	}
}

// Update seals one endpoint onto the chain row. An unknown chain is
// ErrNotFound before the platform-admin check. A caller who is not a
// platform admin is ErrPlatformForbidden. An empty URL is a ValidationError
// and does not wipe the stored endpoint. The answer is rpcUrlSet and does
// not include the URL.
func (s *RPC) Update(ctx context.Context, actorID uuid.UUID, chainID string, body map[string]json.RawMessage) (RPCView, error) {
	if ctx == nil {
		return RPCView{}, fmt.Errorf("update chain rpc: context is required")
	}
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return RPCView{}, fmt.Errorf("update chain rpc: chain id is required")
	}
	if s == nil || s.store == nil || s.admins == nil || s.activity == nil || s.sealer == nil || s.dialer == nil {
		return RPCView{}, fmt.Errorf("chain rpc: service is not configured")
	}
	chain, err := s.store.FindByID(ctx, chainID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return RPCView{}, ErrNotFound
		}
		return RPCView{}, err
	}
	if chain == nil || chain.ID == "" {
		return RPCView{}, ErrNotFound
	}
	if actorID == uuid.Nil {
		return RPCView{}, fmt.Errorf("update chain rpc: actor is required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return RPCView{}, err
	}
	if !admin {
		return RPCView{}, ErrPlatformForbidden
	}
	if err := requireChainPair(ChainThresholdCatalog()); err != nil {
		return RPCView{}, err
	}
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	plaintext, invalid := validateRPCDocument(body)
	if invalid != nil && !invalid.empty() {
		return RPCView{}, invalid
	}
	sealed, err := s.sealer.Seal(plaintext)
	if err != nil || strings.TrimSpace(sealed) == "" || sealed == plaintext {
		return RPCView{}, fmt.Errorf("seal chain rpc")
	}
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.store.UpdateRPCURL(ctx, chain.ID, sealed); err != nil {
			return err
		}
		meta, err := activitylog.ChainRPCChange(chain.ID)
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: actorID,
			Action:      activitylog.ActionChainsUpdated,
			TargetType:  activitylog.TargetChain,
			TargetID:    chain.ID,
			Metadata:    meta,
		})
	})
	if err != nil {
		return RPCView{}, err
	}
	if err := s.retarget(ctx, chain.ID); err != nil {
		return RPCView{}, err
	}
	return RPCView{RPCURLSet: true}, nil
}

// retarget opens the row that was just written and points the dialer at it.
// A concrete URL is used as stored. The environment is not consulted unless
// the stored plaintext is still an env:NAME reference.
func (s *RPC) retarget(ctx context.Context, chainID string) error {
	stored, err := s.store.FindByID(ctx, chainID)
	if err != nil {
		return fmt.Errorf("open chain rpc")
	}
	if stored == nil || stored.RpcURL == "" {
		return fmt.Errorf("open chain rpc")
	}
	opened, err := s.sealer.Open(stored.RpcURL)
	if err != nil {
		return fmt.Errorf("open chain rpc")
	}
	endpoint, err := models.DialEndpoint(opened)
	if err != nil {
		return fmt.Errorf("open chain rpc")
	}
	if _, err := s.dialer.ReplaceEndpoint(chainID, endpoint); err != nil {
		return fmt.Errorf("open chain rpc")
	}
	return nil
}

func validateRPCDocument(body map[string]json.RawMessage) (string, *ValidationError) {
	invalid := &ValidationError{}
	raw, ok := body[fieldRPCURL]
	if !ok {
		invalid.add(fieldRPCURL, "is required")
	}
	for key := range body {
		if key != fieldRPCURL {
			invalid.add(key, "unknown field")
		}
	}
	if !invalid.empty() {
		return "", invalid
	}
	value, message := parseRPCURL(raw)
	if message != "" {
		invalid.add(fieldRPCURL, message)
		return "", invalid
	}
	return value, nil
}

func parseRPCURL(raw json.RawMessage) (string, string) {
	text, kind, message := jsonScalar(raw, "must be a string")
	if message != "" {
		return "", message
	}
	if kind == kindNull {
		return "", "must not be null"
	}
	if kind != kindString {
		return "", "must be a string"
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "must not be empty"
	}
	if strings.HasPrefix(text, models.RPCURLEnvPrefix) {
		if !models.IsValidRPCURLEnvReference(text) {
			return "", "must look like env:NAME"
		}
		return text, ""
	}
	if !httpURL(text) {
		return "", "must be an http or https URL"
	}
	return text, ""
}

func httpURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
